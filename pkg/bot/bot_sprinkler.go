// Package bot implements the coordination logic between GitHub, Slack, and notifications.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
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
// This is the recommended approach as it handles all WebSocket complexity including ping/pong.
func (c *Coordinator) RunWithSprinklerClient(ctx context.Context) error {
	slog.Info("starting bot coordinator with sprinkler client")

	// Get the organization from GitHub
	organization := c.github.Organization()
	if organization == "" {
		return errors.New("failed to detect organization from GitHub installation")
	}

	// Get fresh token
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

			// Log the raw event data for debugging
			slog.Debug("sprinkler raw event data", "raw", event.Raw)

			// The event.Raw should contain the GitHub webhook payload as map[string]any
			var payload json.RawMessage
			if event.Raw != nil {
				// Convert the map to JSON bytes
				if payloadData, err := json.Marshal(event.Raw); err == nil {
					payload = payloadData
					slog.Debug("marshaled payload from sprinkler event",
						"payload_size", len(payload),
						"raw_type", fmt.Sprintf("%T", event.Raw))
				} else {
					slog.Error("failed to marshal event.Raw to JSON payload",
						"error", err,
						"raw_type", fmt.Sprintf("%T", event.Raw),
						"event_type", event.Type,
						"event_url", event.URL,
						"raw_keys", getMapKeys(event.Raw))
					return
				}
			} else {
				slog.Warn("event.Raw is nil - no GitHub webhook payload available")
				// If we don't have the full payload, we can't process this event properly
				slog.Error("cannot process event without GitHub webhook payload",
					"type", event.Type,
					"url", event.URL,
					"needs_full_webhook_data", true)
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
				Type:    event.Type,
				Event:   event.Type,
				Repo:    repo,
				Payload: payload,
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
	slog.Info("starting sprinkler client")
	if err := sprinklerClient.Start(ctx); err != nil {
		// Check if it's an authentication error
		if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "401") {
			slog.Error("authentication failed, refreshing token")
			if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
				slog.Error("failed to refresh token", "error", refreshErr)
			}
			// Try once more with fresh token
			githubToken = c.github.InstallationToken(ctx)
			clientConfig.Token = githubToken
			sprinklerClient, err = client.New(clientConfig)
			if err != nil {
				return fmt.Errorf("failed to create sprinkler client after token refresh: %w", err)
			}
			if err := sprinklerClient.Start(ctx); err != nil {
				return fmt.Errorf("failed to start sprinkler client after token refresh: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to start sprinkler client: %w", err)
	}

	return nil
}
