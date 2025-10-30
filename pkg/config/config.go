// Package config manages server and repository configurations.
package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/google/go-github/v50/github"
	"gopkg.in/yaml.v3"
)

// Constants for logging fields.
const (
	logFieldOrg = "org"
)

// Constants for configuration defaults.
const (
	defaultReminderDMDelayMinutes = 65               // Default delay in minutes before sending DM if user tagged in channel
	defaultConfigCacheTTL         = 20 * time.Minute // Default TTL for configuration cache entries
)

// ServerConfig holds the server configuration from environment variables.
type ServerConfig struct {
	DataDir               string
	SlackSigningSecret    string
	GitHubAppID           string
	GitHubPrivateKey      string
	SprinklerURL          string
	AllowPersonalAccounts bool // Allow processing GitHub personal accounts (default: false for DoS protection)
}

// RepoConfig represents the slack.yaml configuration for a GitHub org.
type RepoConfig struct {
	Channels map[string]struct {
		ReminderDMDelay *int     `yaml:"reminder_dm_delay"` // Optional: override global delay for this channel (0 = disabled)
		Repos           []string `yaml:"repos"`
		Mute            bool     `yaml:"mute"`
	} `yaml:"channels"`
	Global struct {
		TeamID          string `yaml:"team_id"`
		EmailDomain     string `yaml:"email_domain"`
		ReminderDMDelay int    `yaml:"reminder_dm_delay"` // Minutes to wait before sending DM if user tagged in channel (0 = disabled)
		DailyReminders  bool   `yaml:"daily_reminders"`
	} `yaml:"global"`
}

// configCacheEntry represents a cached configuration entry.
type configCacheEntry struct {
	config    *RepoConfig
	timestamp time.Time
}

// configCache manages configuration caching with TTL and thread safety.
// Statistics counters use atomic operations to avoid races during concurrent reads.
type configCache struct {
	entries map[string]configCacheEntry
	ttl     time.Duration
	mu      sync.RWMutex
	hits    atomic.Int64 // Atomic to avoid race during concurrent get() calls
	misses  atomic.Int64 // Atomic to avoid race during concurrent get() calls
}

// get retrieves a cached configuration if it exists and is not expired.
func (c *configCache) get(org string) (*RepoConfig, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[org]
	if !exists {
		c.misses.Add(1)
		return nil, false
	}

	if time.Since(entry.timestamp) > c.ttl {
		c.misses.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	return entry.config, true
}

// set stores a configuration in the cache.
func (c *configCache) set(org string, config *RepoConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[org] = configCacheEntry{
		config:    config,
		timestamp: time.Now(),
	}
}

// invalidate removes a specific organization's config from the cache.
func (c *configCache) invalidate(org string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, org)
	slog.Info("invalidated config cache for organization", "org", org)
}

// invalidateAll clears the entire cache.
func (c *configCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]configCacheEntry)
	slog.Info("invalidated entire config cache")
}

// stats returns cache statistics.
// No lock needed - atomic reads are inherently thread-safe.
func (c *configCache) stats() (hits, misses int64) {
	return c.hits.Load(), c.misses.Load()
}

// Manager manages repository configurations.
type Manager struct {
	configs       map[string]*RepoConfig
	clients       map[string]any // org -> client (stored as any for testability)
	cache         *configCache
	workspaceName string
	mu            sync.RWMutex
}

// New creates a new config manager.
func New() *Manager {
	return &Manager{
		configs: make(map[string]*RepoConfig),
		clients: make(map[string]any),
		cache: &configCache{
			entries: make(map[string]configCacheEntry),
			ttl:     defaultConfigCacheTTL,
		},
	}
}

// SetWorkspaceName sets the Slack workspace name for validation.
func (m *Manager) SetWorkspaceName(workspaceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workspaceName = workspaceName
}

// SetGitHubClient sets the GitHub client for a specific org.
func (m *Manager) SetGitHubClient(org string, client any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[org] = client
}

// createDefaultConfig creates a default RepoConfig with standard settings.
func createDefaultConfig() *RepoConfig {
	return &RepoConfig{
		Channels: make(map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}),
		Global: struct {
			TeamID          string `yaml:"team_id"`
			EmailDomain     string `yaml:"email_domain"`
			ReminderDMDelay int    `yaml:"reminder_dm_delay"`
			DailyReminders  bool   `yaml:"daily_reminders"`
		}{
			TeamID:          "",
			EmailDomain:     "",
			ReminderDMDelay: defaultReminderDMDelayMinutes,
			DailyReminders:  true,
		},
	}
}

// LoadConfig loads the configuration for a GitHub org with retry logic.
func (m *Manager) LoadConfig(ctx context.Context, org string) error {
	// Check cache first
	if cfg, found := m.cache.get(org); found {
		hits, misses := m.cache.stats()
		slog.Debug("using cached config for organization",
			logFieldOrg, org,
			"cache_hits", hits,
			"cache_misses", misses,
			"cache_hit_ratio", float64(hits)/float64(hits+misses))

		m.mu.Lock()
		m.configs[org] = cfg
		m.mu.Unlock()
		return nil
	}

	slog.Info("starting config load for organization",
		logFieldOrg, org,
		"config_repo", ".codeGROOVE",
		"config_file", "slack.yaml",
		"workspace_validation", m.workspaceName != "",
		"cache_miss", true)

	m.mu.Lock()
	defer m.mu.Unlock()

	clientAny := m.clients[org]
	if clientAny == nil {
		return fmt.Errorf("github client not initialized for org: %s", org)
	}

	client, ok := clientAny.(*github.Client)
	if !ok {
		return fmt.Errorf("invalid github client type for org: %s", org)
	}

	var content *github.RepositoryContent
	var configContent string

	// Fetch the config file with retry
	slog.Debug("fetching config file from GitHub",
		logFieldOrg, org,
		"repo", ".codeGROOVE",
		"file", "slack.yaml",
		"retry_attempts", 3)

	err := retry.Do(
		func() error {
			var err error
			content, _, _, err = client.Repositories.GetContents(
				ctx,
				org,
				".codeGROOVE",
				"slack.yaml",
				nil,
			)
			if err != nil {
				// Check if it's a 404 - config might not exist yet
				var ghErr *github.ErrorResponse
				if errors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusNotFound {
					slog.Info("config file not found - org has no configuration",
						logFieldOrg, org,
						"repo", ".codeGROOVE",
						"file", "slack.yaml",
						"will_use_defaults", true)
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to fetch config file, will retry",
					logFieldOrg, org,
					"repo", ".codeGROOVE",
					"file", "slack.yaml",
					"error", err)
				return err
			}

			if content == nil || content.Content == nil {
				slog.Warn("config file exists but is empty",
					logFieldOrg, org,
					"will_use_defaults", true)
				return retry.Unrecoverable(errors.New("config file empty"))
			}

			// Decode the content
			configContent, err = content.GetContent()
			if err != nil {
				slog.Error("failed to decode config file content",
					logFieldOrg, org,
					"error", err)
				return err
			}

			slog.Info("successfully fetched config file",
				logFieldOrg, org,
				"config_size_bytes", len(configContent),
				"will_parse_yaml", true)

			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		// Use default empty config if not found
		cfg := createDefaultConfig()
		m.configs[org] = cfg
		m.cache.set(org, cfg)

		hits, misses := m.cache.stats()
		slog.Info("using default configuration for org",
			logFieldOrg, org,
			"reason", "config_load_failed",
			"error", err,
			"default_daily_reminders", true,
			"cached", true,
			"cache_hits", hits,
			"cache_misses", misses)
		return nil // Graceful degradation
	}

	// Parse the YAML
	slog.Debug("parsing YAML configuration",
		logFieldOrg, org,
		"config_content_preview", configContent[:min(len(configContent), 200)])

	var config RepoConfig
	if err := yaml.Unmarshal([]byte(configContent), &config); err != nil {
		cfg := createDefaultConfig()
		m.configs[org] = cfg
		m.cache.set(org, cfg)

		hits, misses := m.cache.stats()
		slog.Error("failed to parse YAML configuration - using defaults",
			logFieldOrg, org,
			"yaml_error", err,
			"config_preview", configContent[:min(len(configContent), 100)],
			"will_use_defaults", true,
			"cached", true,
			"cache_hits", hits,
			"cache_misses", misses)
		return nil // Graceful degradation
	}

	slog.Info("successfully parsed YAML configuration",
		logFieldOrg, org,
		"parsed_channels", len(config.Channels),
		"has_global_config", true,
		"team_id", config.Global.TeamID,
		"email_domain", config.Global.EmailDomain)

	// Count channel configurations
	var muted, repos, wildcard int
	for name, ch := range config.Channels {
		if ch.Mute {
			muted++
		}
		repos += len(ch.Repos)

		hasWild := false
		for _, repo := range ch.Repos {
			if repo == "*" {
				wildcard++
				hasWild = true
				break
			}
		}

		slog.Debug("channel configuration loaded",
			logFieldOrg, org,
			"channel", name,
			"repos_count", len(ch.Repos),
			"repos", ch.Repos,
			"muted", ch.Mute,
			"has_wildcard", hasWild)
	}

	m.configs[org] = &config

	// Cache the configuration
	m.cache.set(org, &config)

	hits, misses := m.cache.stats()
	slog.Info("configuration successfully loaded and validated",
		logFieldOrg, org,
		"final_config", map[string]any{
			"team_id":             config.Global.TeamID,
			"email_domain":        config.Global.EmailDomain,
			"daily_reminders":     config.Global.DailyReminders,
			"reminder_dm_delay":   config.Global.ReminderDMDelay,
			"total_channels":      len(config.Channels),
			"muted_channels":      muted,
			"wildcard_channels":   wildcard,
			"total_repo_mappings": repos,
		},
		"cached", true,
		"cache_hits", hits,
		"cache_misses", misses)

	return nil
}

// Config returns the configuration for a GitHub org.
func (m *Manager) Config(org string) (*RepoConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, exists := m.configs[org]
	return config, exists
}

// ChannelsForRepo returns the Slack channels configured for a specific repo.
// It first checks explicit YAML configuration, then falls back to auto-discovery
// where a repo named "goose" would automatically map to channel "#goose".
func (m *Manager) ChannelsForRepo(org, repo string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		// No config available, try auto-discovery
		return m.autoDiscoverChannels(org, repo)
	}

	var channels []string
	var explicitlyConfigured bool

	// First, check explicit YAML configuration
	for channelName, channelConfig := range config.Channels {
		// Normalize channel name to lowercase (Slack channels are always lowercase)
		normalizedChannelName := strings.ToLower(channelName)

		// Check if this channel explicitly includes this repo
		for _, configRepo := range channelConfig.Repos {
			if m.matchesRepo(configRepo, repo) {
				explicitlyConfigured = true
				// Skip muted channels even if explicitly configured
				if channelConfig.Mute {
					slog.Debug("skipping explicitly muted channel",
						logFieldOrg, org,
						"repo", repo,
						"channel", normalizedChannelName,
						"muted", true)
					continue
				}
				channels = append(channels, normalizedChannelName)
				break // Don't add the same channel multiple times
			}
		}
	}

	// ALWAYS try auto-discovery for the repo-named channel unless explicitly muted
	autoChannels := m.autoDiscoverChannels(org, repo)
	for _, autoChannel := range autoChannels {
		// Check if this channel is already included in explicit config
		alreadyIncluded := false
		for _, existingChannel := range channels {
			if existingChannel == autoChannel {
				alreadyIncluded = true
				break
			}
		}

		if alreadyIncluded {
			slog.Debug("auto-discovered channel already included via explicit config",
				logFieldOrg, org,
				"repo", repo,
				"channel", autoChannel)
			continue
		}

		// Check if auto-discovered channel is explicitly muted
		// Need to check case-insensitively since YAML keys preserve case
		var channelMuted bool
		for yamlChannelName, channelConfig := range config.Channels {
			if strings.EqualFold(yamlChannelName, autoChannel) && channelConfig.Mute {
				channelMuted = true
				break
			}
		}
		if channelMuted {
			slog.Info("auto-discovered channel is explicitly muted in config",
				logFieldOrg, org,
				"repo", repo,
				"channel", autoChannel,
				"muted", true)
			continue
		}

		// Add the auto-discovered channel
		channels = append(channels, autoChannel)
		slog.Debug("added auto-discovered channel",
			logFieldOrg, org,
			"repo", repo,
			"channel", autoChannel,
			"reason", "repo name matches channel name")
	}

	if len(channels) > 0 {
		slog.Debug("resolved channels for repo",
			logFieldOrg, org,
			"repo", repo,
			"channels", channels,
			"has_explicit_config", explicitlyConfigured,
			"includes_auto_discovery", true)
	}

	return channels
}

// matchesRepo checks if a repo name matches a pattern (supports * wildcard).
func (*Manager) matchesRepo(pattern, repo string) bool {
	// Simple wildcard matching - "*" matches everything
	if pattern == "*" {
		return true
	}

	// Exact match
	if pattern == repo {
		return true
	}

	// TODO: Could add more sophisticated pattern matching here
	// For now, only support exact match and "*"
	return false
}

// autoDiscoverChannels automatically discovers Slack channels based on repo names.
// For example: repo "goose" -> channel "#goose", repo "my-service" -> channel "#my-service".
func (*Manager) autoDiscoverChannels(org, repo string) []string {
	// Convert repo name to channel name
	// Slack channel names are always lowercase
	channelName := strings.ToLower(repo)

	slog.Debug("attempting auto-discovery of channel for repo",
		logFieldOrg, org,
		"repo", repo,
		"auto_channel", channelName,
		"explanation", "repo name matches Slack channel name convention")

	return []string{channelName}
}

// Domain returns the email domain for GitHub-to-Slack user mapping for the given organization.
func (m *Manager) Domain(org string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		return ""
	}
	return config.Global.EmailDomain
}

// IsChannelMuted checks if a specific channel is muted for an org.
func (m *Manager) IsChannelMuted(org, channel string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		return false
	}

	if channelConfig, ok := config.Channels[channel]; ok {
		return channelConfig.Mute
	}
	return false
}

// DailyRemindersEnabled returns whether daily reminders are enabled for an org.
func (m *Manager) DailyRemindersEnabled(org string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		return true // Default
	}
	return config.Global.DailyReminders
}

// ReminderDMDelay returns the follow-up reminder delay in minutes for a specific channel.
// Returns the channel-specific delay if set, otherwise the global delay (default: 65 minutes).
// A value of 0 means the feature is disabled.
func (m *Manager) ReminderDMDelay(org, channel string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		slog.Debug("no config for org - using default delay",
			logFieldOrg, org,
			"default_delay_mins", defaultReminderDMDelayMinutes)
		return defaultReminderDMDelayMinutes
	}

	// Check for channel-specific override
	if channelConfig, ok := config.Channels[channel]; ok {
		if channelConfig.ReminderDMDelay != nil {
			slog.Debug("using channel-specific reminder delay",
				logFieldOrg, org,
				"channel", channel,
				"delay_mins", *channelConfig.ReminderDMDelay)
			return *channelConfig.ReminderDMDelay
		}
	}

	// Return global setting (or default if not set)
	if config.Global.ReminderDMDelay > 0 {
		slog.Debug("using global reminder delay",
			logFieldOrg, org,
			"channel", channel,
			"delay_mins", config.Global.ReminderDMDelay)
		return config.Global.ReminderDMDelay
	}
	slog.Debug("global delay is 0 or unset - using default",
		logFieldOrg, org,
		"channel", channel,
		"global_value", config.Global.ReminderDMDelay,
		"default_delay_mins", defaultReminderDMDelayMinutes)
	return defaultReminderDMDelayMinutes
}

// ReloadConfig reloads the configuration for an org (e.g., when .codeGROOVE repo is updated).
func (m *Manager) ReloadConfig(ctx context.Context, org string) error {
	slog.Info("reloading config", "org", org)
	// Invalidate cache first to force fresh load
	m.cache.invalidate(org)
	return m.LoadConfig(ctx, org)
}

// InvalidateConfig removes the cached configuration for a specific organization.
// This is used when .codeGROOVE repository events are received to ensure fresh config loading.
func (m *Manager) InvalidateConfig(org string) {
	m.cache.invalidate(org)
}

// InvalidateAllConfigs removes all cached configurations.
// This can be useful during testing or major configuration changes.
func (m *Manager) InvalidateAllConfigs() {
	m.cache.invalidateAll()
}

// CacheStats returns cache hit and miss statistics for monitoring performance.
func (m *Manager) CacheStats() (hits, misses int64) {
	return m.cache.stats()
}

// WorkspaceName returns the Slack workspace team_id for an organization.
// Returns empty string if no workspace is configured.
func (m *Manager) WorkspaceName(org string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		return ""
	}
	return config.Global.TeamID
}
