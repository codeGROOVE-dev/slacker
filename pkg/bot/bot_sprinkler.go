// Package bot implements the coordination logic between GitHub, Slack, and notifications.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
)

// Constants for URL parsing.
const (
	githubURLMinParts = 7
)

// getMapKeys returns the keys of a map[string]any for logging purposes.
func getMapKeys(m map[string]any) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// RunWithSprinklerClient runs the bot using the official sprinkler client library.
func (c *Coordinator) RunWithSprinklerClient(ctx context.Context) error {
	slog.Info("starting bot coordinator with sprinkler client")

	organization := c.github.Organization()
	if organization == "" {
		return errors.New("failed to detect organization from GitHub installation")
	}

	githubToken := c.github.InstallationToken(ctx)
	if githubToken == "" {
		return errors.New("no GitHub token available")
	}

	// Create sprinkler client configuration
	clientConfig := client.Config{
		ServerURL:    c.sprinklerURL,
		Organization: organization,
		Token:        githubToken,
		EventTypes:   []string{"*"}, // Subscribe to all event types
		Logger:       slog.Default(),
		Verbose:      true,
		NoReconnect:  false, // Enable automatic reconnection
		PingInterval: 0,     // Use default (30 seconds)
		OnConnect: func() {
			slog.Info("sprinkler client connected",
				"organization", organization,
				"url", c.sprinklerURL)
		},
		OnDisconnect: func(err error) {
			if err != nil {
				slog.Error("sprinkler client disconnected", "error", err)
				return
			}
			slog.Info("sprinkler client disconnected normally")
		},
		OnEvent: func(event client.Event) {
			slog.Info("processing sprinkler event",
				"type", event.Type,
				"url", event.URL,
				"timestamp", event.Timestamp)

			// Log the sprinkler event data for debugging
			slog.Debug("sprinkler event metadata",
				"type", event.Type,
				"url", event.URL,
				"timestamp", event.Timestamp,
				"raw_keys", getMapKeys(event.Raw))

			// Sprinkler only provides metadata - extract PR number from URL
			if event.URL == "" {
				slog.Error("sprinkler event missing URL - cannot determine PR number",
					"type", event.Type)
				return
			}

			// Parse URL like https://github.com/owner/repo/pull/123
			parts := strings.Split(event.URL, "/")
			if len(parts) < githubURLMinParts || parts[2] != "github.com" || parts[5] != "pull" {
				slog.Error("invalid GitHub URL format from sprinkler",
					"url", event.URL,
					"expected_format", "https://github.com/owner/repo/pull/123")
				return
			}

			num, err := strconv.Atoi(parts[6])
			if err != nil {
				slog.Error("failed to parse PR number from URL",
					"url", event.URL,
					"parse_error", err)
				return
			}

			prNumber := num
			slog.Debug("extracted PR number from URL",
				"pr_number", prNumber,
				"url", event.URL)

			if prNumber == 0 {
				slog.Error("invalid PR number extracted from sprinkler URL",
					"url", event.URL,
					"extracted_number", prNumber)
				return
			}

			// Extract repo from URL if possible
			repo := ""
			if event.URL != "" {
				// Parse URL like https://github.com/owner/repo/pull/123
				parts := strings.Split(event.URL, "/")
				if len(parts) >= 5 && parts[2] == "github.com" {
					repo = parts[3] + "/" + parts[4]
				}
			}

			if repo == "" {
				slog.Error("could not extract repo from URL",
					"url", event.URL,
					"cannot_process_without_repo", true)
				return
			}

			msg := SprinklerMessage{
				Type:      event.Type,
				Event:     event.Type,
				Repo:      repo,
				PRNumber:  prNumber,
				URL:       event.URL,
				Timestamp: event.Timestamp,
			}

			if err := c.processEvent(ctx, msg); err != nil {
				slog.Error("error processing event",
					"error", err,
					"type", event.Type,
					"url", event.URL,
					"repo", repo)
			}
		},
	}

	// Create the sprinkler client
	sprinklerClient, err := client.New(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to create sprinkler client: %w", err)
	}

	// Start the client (this will handle reconnection, ping/pong, etc.)
	// Note: Start() may return when connection is lost, so we loop to restart it
	slog.Info("starting sprinkler client")

	for {
		if err := sprinklerClient.Start(ctx); err != nil {
			// Context cancelled - clean shutdown
			if errors.Is(err, context.Canceled) {
				slog.Info("sprinkler client context cancelled")
				return nil
			}

			// Check if it's an authentication error
			if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "401") {
				slog.Warn("authentication failed, refreshing token")
				if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
					slog.Error("failed to refresh token", "error", refreshErr)
					return err
				}
				// Try once more with fresh token
				githubToken = c.github.InstallationToken(ctx)
				clientConfig.Token = githubToken
				newClient, err := client.New(clientConfig)
				if err != nil {
					return fmt.Errorf("failed to create sprinkler client after token refresh: %w", err)
				}
				sprinklerClient = newClient
				continue
			}

			// Other error - log and retry after delay
			slog.Warn("sprinkler client stopped, will restart",
				"error", err,
				"delay_seconds", 5)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				slog.Info("restarting sprinkler client")
				continue
			}
		}

		// Start() returned nil - should not happen normally
		slog.Warn("sprinkler client Start() returned nil unexpectedly")
		return nil
	}
}
