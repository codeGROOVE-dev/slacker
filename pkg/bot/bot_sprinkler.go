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

			// Convert the raw event data to our SprinklerMessage format
			// The event.Raw contains the GitHub webhook payload
			var payload json.RawMessage
			if payloadData, err := json.Marshal(event.Raw); err == nil {
				payload = payloadData
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
					"url", event.URL)
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
