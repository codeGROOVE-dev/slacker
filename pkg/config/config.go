// Package config manages server and repository configurations.
package config

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/google/go-github/v50/github"
	"gopkg.in/yaml.v3"
)

// ServerConfig holds the server configuration from environment variables.
type ServerConfig struct {
	DataDir              string
	SlackToken           string
	SlackSigningSecret   string
	GitHubAppID          string
	GitHubPrivateKey     string
	GitHubInstallationID string
	SprinklerURL         string
}

// RepoConfig represents the slack.yaml configuration for a GitHub org.
type RepoConfig struct {
	Channels map[string]struct {
		Repos []string `yaml:"repos"`
		Mute  bool     `yaml:"mute"`
	} `yaml:"channels"`
	Global struct {
		Prefix                 string `yaml:"prefix"`
		Slack                  string `yaml:"slack"`
		ChannelNotifyDelayMins int    `yaml:"channel_notify_delay_mins"`
		DailyReminders         bool   `yaml:"daily_reminders"`
	} `yaml:"global"` // Default: 60
	// Default: true
}

// configCacheEntry represents a cached configuration entry.
type configCacheEntry struct {
	config    *RepoConfig
	timestamp time.Time
}

// configCache manages configuration caching with TTL and thread safety.
type configCache struct {
	entries map[string]configCacheEntry
	ttl     time.Duration
	mu      sync.RWMutex
	hits    int64
	misses  int64
}

// newConfigCache creates a new configuration cache with specified TTL.
func newConfigCache(ttl time.Duration) *configCache {
	return &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     ttl,
	}
}

// get retrieves a cached configuration if it exists and is not expired.
func (c *configCache) get(org string) (*RepoConfig, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[org]
	if !exists {
		c.misses++
		return nil, false
	}

	if time.Since(entry.timestamp) > c.ttl {
		c.misses++
		return nil, false
	}

	c.hits++
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
func (c *configCache) stats() (hits, misses int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses
}

// Manager manages repository configurations.
type Manager struct {
	configs       map[string]*RepoConfig
	client        *github.Client
	cache         *configCache
	workspaceName string
	mu            sync.RWMutex
}

// New creates a new config manager.
func New(ctx context.Context) *Manager {
	return &Manager{
		configs: make(map[string]*RepoConfig),
		cache:   newConfigCache(20 * time.Minute), // 20-minute TTL as requested
	}
}

// SetWorkspaceName sets the Slack workspace name for validation.
func (m *Manager) SetWorkspaceName(workspaceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workspaceName = workspaceName
}

// SetGitHubClient sets the GitHub client for fetching configs.
func (m *Manager) SetGitHubClient(client *github.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = client
}

// LoadConfig loads the configuration for a GitHub org with retry logic.
func (m *Manager) LoadConfig(ctx context.Context, org string) error {
	// Check cache first
	if cachedConfig, found := m.cache.get(org); found {
		hits, misses := m.cache.stats()
		slog.Debug("using cached config for organization",
			"org", org,
			"cache_hits", hits,
			"cache_misses", misses,
			"cache_hit_ratio", float64(hits)/float64(hits+misses))

		m.mu.Lock()
		m.configs[org] = cachedConfig
		m.mu.Unlock()
		return nil
	}

	slog.Info("starting config load for organization",
		"org", org,
		"config_repo", ".codeGROOVE",
		"config_file", "slack.yaml",
		"workspace_validation", m.workspaceName != "",
		"cache_miss", true)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client == nil {
		return errors.New("github client not initialized")
	}

	var content *github.RepositoryContent
	var configContent string

	// Fetch the config file with retry
	slog.Debug("fetching config file from GitHub",
		"org", org,
		"repo", ".codeGROOVE",
		"file", "slack.yaml",
		"retry_attempts", 3)

	err := retry.Do(
		func() error {
			var err error
			content, _, _, err = m.client.Repositories.GetContents(
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
						"org", org,
						"repo", ".codeGROOVE",
						"file", "slack.yaml",
						"will_use_defaults", true)
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to fetch config file, will retry",
					"org", org,
					"repo", ".codeGROOVE",
					"file", "slack.yaml",
					"error", err)
				return err
			}

			if content == nil || content.Content == nil {
				slog.Warn("config file exists but is empty",
					"org", org,
					"will_use_defaults", true)
				return retry.Unrecoverable(errors.New("config file empty"))
			}

			// Decode the content
			configContent, err = content.GetContent()
			if err != nil {
				slog.Error("failed to decode config file content",
					"org", org,
					"error", err)
				return err
			}

			slog.Info("successfully fetched config file",
				"org", org,
				"config_size_bytes", len(configContent),
				"will_parse_yaml", true)

			return nil
		},
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		// Use default empty config if not found
		defaultConfig := &RepoConfig{
			Global: struct {
				Prefix                 string `yaml:"prefix"`
				Slack                  string `yaml:"slack"`
				ChannelNotifyDelayMins int    `yaml:"channel_notify_delay_mins"`
				DailyReminders         bool   `yaml:"daily_reminders"`
			}{
				Prefix:                 ":postal_horn:",
				Slack:                  "",
				ChannelNotifyDelayMins: 60,
				DailyReminders:         true,
			},
			Channels: make(map[string]struct {
				Repos []string `yaml:"repos"`
				Mute  bool     `yaml:"mute"`
			}),
		}
		m.configs[org] = defaultConfig
		m.cache.set(org, defaultConfig)

		hits, misses := m.cache.stats()
		slog.Info("using default configuration for org",
			"org", org,
			"reason", "config_load_failed",
			"error", err,
			"default_prefix", ":postal_horn:",
			"default_delay_mins", 60,
			"default_daily_reminders", true,
			"cached", true,
			"cache_hits", hits,
			"cache_misses", misses)
		return nil // Graceful degradation
	}

	// Parse the YAML
	slog.Debug("parsing YAML configuration",
		"org", org,
		"config_content_preview", configContent[:min(len(configContent), 200)])

	var config RepoConfig
	if err := yaml.Unmarshal([]byte(configContent), &config); err != nil {
		defaultConfig := &RepoConfig{
			Global: struct {
				Prefix                 string `yaml:"prefix"`
				Slack                  string `yaml:"slack"`
				ChannelNotifyDelayMins int    `yaml:"channel_notify_delay_mins"`
				DailyReminders         bool   `yaml:"daily_reminders"`
			}{
				Prefix:                 ":postal_horn:",
				Slack:                  "",
				ChannelNotifyDelayMins: 60,
				DailyReminders:         true,
			},
			Channels: make(map[string]struct {
				Repos []string `yaml:"repos"`
				Mute  bool     `yaml:"mute"`
			}),
		}
		m.configs[org] = defaultConfig
		m.cache.set(org, defaultConfig)

		hits, misses := m.cache.stats()
		slog.Error("failed to parse YAML configuration - using defaults",
			"org", org,
			"yaml_error", err,
			"config_preview", configContent[:min(len(configContent), 100)],
			"will_use_defaults", true,
			"cached", true,
			"cache_hits", hits,
			"cache_misses", misses)
		return nil // Graceful degradation
	}

	slog.Info("successfully parsed YAML configuration",
		"org", org,
		"parsed_channels", len(config.Channels),
		"has_global_config", true,
		"workspace_specified", config.Global.Slack != "")

	// Validate workspace name matches if specified
	if config.Global.Slack != "" && m.workspaceName != "" {
		slog.Debug("validating workspace name",
			"org", org,
			"config_workspace", config.Global.Slack,
			"actual_workspace", m.workspaceName,
			"validation_enabled", true)

		if config.Global.Slack != m.workspaceName {
			// Return empty config for workspace mismatch
			emptyConfig := &RepoConfig{
				Global: struct {
					Prefix                 string `yaml:"prefix"`
					Slack                  string `yaml:"slack"`
					ChannelNotifyDelayMins int    `yaml:"channel_notify_delay_mins"`
					DailyReminders         bool   `yaml:"daily_reminders"`
				}{
					Prefix:                 ":postal_horn:",
					Slack:                  "",
					ChannelNotifyDelayMins: 60,
					DailyReminders:         true,
				},
				Channels: make(map[string]struct {
					Repos []string `yaml:"repos"`
					Mute  bool     `yaml:"mute"`
				}),
			}
			m.configs[org] = emptyConfig
			m.cache.set(org, emptyConfig)

			hits, misses := m.cache.stats()
			slog.Warn("workspace mismatch - config is for different Slack workspace",
				"org", org,
				"config_workspace", config.Global.Slack,
				"actual_workspace", m.workspaceName,
				"action", "skipping_config",
				"will_use_empty_config", true,
				"notifications_will_be_disabled", true,
				"cached", true,
				"cache_hits", hits,
				"cache_misses", misses)
			return nil
		}

		slog.Info("workspace validation successful",
			"org", org,
			"workspace", m.workspaceName,
			"config_valid_for_workspace", true)
	} else {
		if config.Global.Slack == "" {
			slog.Info("no workspace validation required - config has no workspace specified",
				"org", org,
				"will_accept_any_workspace", true)
		} else {
			slog.Warn("workspace validation disabled - no actual workspace name available",
				"org", org,
				"config_workspace", config.Global.Slack,
				"validation_skipped", true)
		}
	}

	// Set defaults and validate configuration
	originalPrefix := config.Global.Prefix
	originalDelay := config.Global.ChannelNotifyDelayMins
	originalReminders := config.Global.DailyReminders

	if config.Global.Prefix == "" {
		config.Global.Prefix = ":postal_horn:"
	}
	if config.Global.ChannelNotifyDelayMins == 0 {
		config.Global.ChannelNotifyDelayMins = 60
	}
	if !config.Global.DailyReminders {
		config.Global.DailyReminders = true // YAML defaults to false for bool
	}

	defaultsApplied := []string{}
	if originalPrefix == "" {
		defaultsApplied = append(defaultsApplied, "prefix")
	}
	if originalDelay == 0 {
		defaultsApplied = append(defaultsApplied, "delay_mins")
	}
	if !originalReminders {
		defaultsApplied = append(defaultsApplied, "daily_reminders")
	}

	// Count channel configurations
	mutedChannels := 0
	totalRepos := 0
	wildcardChannels := 0
	for channelName, channelConfig := range config.Channels {
		if channelConfig.Mute {
			mutedChannels++
		}
		totalRepos += len(channelConfig.Repos)
		for _, repo := range channelConfig.Repos {
			if repo == "*" {
				wildcardChannels++
				break
			}
		}
		slog.Debug("channel configuration loaded",
			"org", org,
			"channel", channelName,
			"repos_count", len(channelConfig.Repos),
			"repos", channelConfig.Repos,
			"muted", channelConfig.Mute,
			"has_wildcard", func() bool {
				for _, r := range channelConfig.Repos {
					if r == "*" {
						return true
					}
				}
				return false
			}())
	}

	m.configs[org] = &config

	// Cache the configuration
	m.cache.set(org, &config)

	hits, misses := m.cache.stats()
	slog.Info("configuration successfully loaded and validated",
		"org", org,
		"final_config", map[string]interface{}{
			"prefix":                    config.Global.Prefix,
			"slack_workspace":           config.Global.Slack,
			"channel_notify_delay_mins": config.Global.ChannelNotifyDelayMins,
			"daily_reminders":           config.Global.DailyReminders,
			"total_channels":            len(config.Channels),
			"muted_channels":            mutedChannels,
			"wildcard_channels":         wildcardChannels,
			"total_repo_mappings":       totalRepos,
			"defaults_applied":          defaultsApplied,
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
		// Check if this channel explicitly includes this repo
		for _, configRepo := range channelConfig.Repos {
			if m.matchesRepo(configRepo, repo) {
				explicitlyConfigured = true
				// Skip muted channels even if explicitly configured
				if channelConfig.Mute {
					slog.Debug("skipping explicitly muted channel",
						"org", org,
						"repo", repo,
						"channel", channelName,
						"muted", true)
					continue
				}
				channels = append(channels, channelName)
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
				"org", org,
				"repo", repo,
				"channel", autoChannel)
			continue
		}

		// Check if auto-discovered channel is explicitly muted
		if channelConfig, exists := config.Channels[autoChannel]; exists && channelConfig.Mute {
			slog.Info("auto-discovered channel is explicitly muted in config",
				"org", org,
				"repo", repo,
				"channel", autoChannel,
				"muted", true)
			continue
		}

		// Add the auto-discovered channel
		channels = append(channels, autoChannel)
		slog.Debug("added auto-discovered channel",
			"org", org,
			"repo", repo,
			"channel", autoChannel,
			"reason", "repo name matches channel name")
	}

	if len(channels) > 0 {
		slog.Debug("resolved channels for repo",
			"org", org,
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
func (m *Manager) autoDiscoverChannels(org, repo string) []string {
	// Convert repo name to channel name
	// Most repos will match their channel name directly
	channelName := repo

	slog.Info("attempting auto-discovery of channel for repo",
		"org", org,
		"repo", repo,
		"auto_channel", channelName,
		"explanation", "repo name matches Slack channel name convention")

	return []string{channelName}
}

// Prefix returns the prefix for messages in an org.
func (m *Manager) Prefix(org string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists || config.Global.Prefix == "" {
		return ":postal_horn:"
	}
	return config.Global.Prefix
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

// ChannelNotifyDelayMins returns the notification delay in minutes for an org.
func (m *Manager) ChannelNotifyDelayMins(org string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		return 60 // Default
	}
	return config.Global.ChannelNotifyDelayMins
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
