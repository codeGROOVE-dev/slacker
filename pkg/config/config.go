package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	Global struct {
		Prefix string `yaml:"prefix"`
	} `yaml:"global"`
	Repos map[string]struct {
		Channels []string `yaml:"channels"`
	} `yaml:"repos"`
}

// Manager manages repository configurations.
type Manager struct {
	mu      sync.RWMutex
	configs map[string]*RepoConfig // org -> config
	client  *github.Client
}

// New creates a new config manager.
func New(ctx context.Context) *Manager {
	return &Manager{
		configs: make(map[string]*RepoConfig),
	}
}

// SetGitHubClient sets the GitHub client for fetching configs.
func (m *Manager) SetGitHubClient(client *github.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = client
}

// LoadConfig loads the configuration for a GitHub org with retry logic.
func (m *Manager) LoadConfig(ctx context.Context, org string) error {
	slog.Info("loading config", "org", org)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client == nil {
		return fmt.Errorf("github client not initialized")
	}

	var content *github.RepositoryContent
	var configContent string

	// Fetch the config file with retry
	err := retry.Do(
		func() error {
			var err error
			content, _, _, err = m.client.Repositories.GetContents(
				ctx,
				org,
				".github",
				"codeGROOVE/slack.yaml",
				nil,
			)
			if err != nil {
				// Check if it's a 404 - config might not exist yet
				var ghErr *github.ErrorResponse
				if errors.As(err, &ghErr) && ghErr.Response.StatusCode == 404 {
					slog.Debug("config file not found, using defaults", "org", org)
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to fetch config, retrying", "org", org, "error", err)
				return err
			}

			if content == nil || content.Content == nil {
				slog.Debug("config file empty", "org", org)
				return retry.Unrecoverable(fmt.Errorf("config file empty"))
			}

			// Decode the content
			configContent, err = content.GetContent()
			if err != nil {
				slog.Warn("failed to decode config content", "error", err)
				return err
			}

			return nil
		},
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		// Use default empty config if not found
		slog.Warn("failed to load config, using empty config", "org", org, "error", err)
		m.configs[org] = &RepoConfig{
			Global: struct {
				Prefix string `yaml:"prefix"`
			}{Prefix: ":postal_horn:"},
			Repos: make(map[string]struct {
				Channels []string `yaml:"channels"`
			}),
		}
		return nil // Graceful degradation
	}

	// Parse the YAML
	var config RepoConfig
	if err := yaml.Unmarshal([]byte(configContent), &config); err != nil {
		slog.Warn("failed to parse config YAML, using empty config", "org", org, "error", err)
		m.configs[org] = &RepoConfig{
			Global: struct {
				Prefix string `yaml:"prefix"`
			}{Prefix: ":postal_horn:"},
			Repos: make(map[string]struct {
				Channels []string `yaml:"channels"`
			}),
		}
		return nil // Graceful degradation
	}

	if config.Global.Prefix == "" {
		config.Global.Prefix = ":postal_horn:"
	}

	m.configs[org] = &config
	slog.Info("successfully loaded config", "org", org, "repos", len(config.Repos))
	return nil
}

// GetConfig returns the configuration for a GitHub org.
func (m *Manager) GetConfig(org string) (*RepoConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, exists := m.configs[org]
	return config, exists
}

// GetChannelsForRepo returns the Slack channels configured for a specific repo.
func (m *Manager) GetChannelsForRepo(org, repo string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists {
		return nil
	}

	if repoConfig, ok := config.Repos[repo]; ok {
		return repoConfig.Channels
	}
	return nil
}

// GetPrefix returns the prefix for messages in an org.
func (m *Manager) GetPrefix(org string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config, exists := m.configs[org]
	if !exists || config.Global.Prefix == "" {
		return ":postal_horn:"
	}
	return config.Global.Prefix
}

// ReloadConfig reloads the configuration for an org (e.g., when .github repo is updated).
func (m *Manager) ReloadConfig(ctx context.Context, org string) error {
	slog.Info("reloading config", "org", org)
	return m.LoadConfig(ctx, org)
}
