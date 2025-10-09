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

	// createClientConfig creates a new sprinkler client config with fresh token.
	// This is a helper to avoid duplicating config setup.
	createClientConfig := func(token string) client.Config {
		return client.Config{
			ServerURL:    c.sprinklerURL,
			Organization: organization,
			Token:        token,
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
					slog.Error("sprinkler client disconnected",
						"organization", organization,
						"error", err)
					return
				}
				slog.Info("sprinkler client disconnected normally",
					"organization", organization)
			},
			OnEvent: func(event client.Event) {
				// Use background context for event processing to avoid losing events
				// during shutdown. The coordinator's context is for the connection
				// lifecycle, not individual events.
				eventCtx := context.Background()

				slog.Info("processing sprinkler event",
					"organization", organization,
					"type", event.Type,
					"url", event.URL,
					"timestamp", event.Timestamp)

				// Log the sprinkler event data for debugging
				var rawKeys []string
				if event.Raw != nil {
					rawKeys = make([]string, 0, len(event.Raw))
					for k := range event.Raw {
						rawKeys = append(rawKeys, k)
					}
				}
				slog.Debug("sprinkler event metadata",
					"organization", organization,
					"type", event.Type,
					"url", event.URL,
					"timestamp", event.Timestamp,
					"raw_keys", rawKeys)

				// Sprinkler only provides metadata - extract PR number from URL
				if event.URL == "" {
					slog.Error("sprinkler event missing URL - cannot determine PR number",
						"organization", organization,
						"type", event.Type)
					return
				}

				// Parse URL like https://github.com/owner/repo/pull/123
				parts := strings.Split(event.URL, "/")
				if len(parts) < githubURLMinParts || parts[2] != "github.com" || parts[5] != "pull" {
					slog.Error("invalid GitHub URL format from sprinkler",
						"organization", organization,
						"url", event.URL,
						"expected_format", "https://github.com/owner/repo/pull/123")
					return
				}

				num, err := strconv.Atoi(parts[6])
				if err != nil {
					slog.Error("failed to parse PR number from URL",
						"organization", organization,
						"url", event.URL,
						"parse_error", err)
					return
				}

				prNumber := num
				slog.Debug("extracted PR number from URL",
					"organization", organization,
					"pr_number", prNumber,
					"url", event.URL)

				if prNumber == 0 {
					slog.Error("invalid PR number extracted from sprinkler URL",
						"organization", organization,
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
						"organization", organization,
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

				if err := c.processEvent(eventCtx, msg); err != nil {
					slog.Error("error processing event",
						"organization", organization,
						"error", err,
						"type", event.Type,
						"url", event.URL,
						"repo", repo)
				}
			},
		}
	}

	// Create the sprinkler client
	sprinklerClient, err := client.New(createClientConfig(githubToken))
	if err != nil {
		return fmt.Errorf("failed to create sprinkler client: %w", err)
	}

	// Start the client (this will handle reconnection, ping/pong, etc.)
	// Note: Start() may return when connection is lost, so we loop to restart it
	slog.Info("starting sprinkler client", "organization", organization)

	retryDelay := 5 * time.Second
	maxRetryDelay := 60 * time.Second
	consecutiveErrors := 0
	connectionAttempts := 0
	var lastError error
	var lastErrorTime time.Time

	for {
		connectionAttempts++
		slog.Info("attempting sprinkler connection",
			"organization", organization,
			"attempt", connectionAttempts,
			"consecutive_errors", consecutiveErrors,
			"retry_delay_seconds", retryDelay.Seconds())

		startErr := sprinklerClient.Start(ctx)

		// Track error for pattern detection
		if startErr != nil {
			lastError = startErr
			lastErrorTime = time.Now()
		}

		// Context cancelled - clean shutdown
		if errors.Is(startErr, context.Canceled) {
			slog.Info("sprinkler client context cancelled, stopping gracefully",
				"organization", organization,
				"total_attempts", connectionAttempts)
			return nil
		}

		// Check if context was cancelled even without error
		// (this handles the case where Start() returns nil on clean shutdown)
		if ctxErr := ctx.Err(); ctxErr != nil {
			slog.Info("context cancelled, stopping sprinkler client",
				"organization", organization,
				"context_error", ctxErr)
			return ctxErr
		}

		// Handle different error types
		if startErr != nil {
			consecutiveErrors++

			// Check if it's an authentication error
			if strings.Contains(startErr.Error(), "403") || strings.Contains(startErr.Error(), "401") {
				slog.Warn("authentication failed, refreshing token",
					"organization", organization,
					"consecutive_errors", consecutiveErrors,
					"error", startErr)

				if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
					slog.Error("failed to refresh token",
						"organization", organization,
						"refresh_error", refreshErr,
						"original_error", startErr)
					// Don't exit - the outer retry loop will try again
					select {
					case <-ctx.Done():
						slog.Info("context cancelled during token refresh retry wait", "organization", organization)
						return ctx.Err()
					case <-time.After(retryDelay):
						continue
					}
				}

				// Get fresh token after successful refresh
				freshToken := c.github.InstallationToken(ctx)
				if freshToken == "" {
					slog.Error("token refresh succeeded but returned empty token",
						"organization", organization,
						"will_retry_with_delay", true)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(retryDelay):
						continue
					}
				}

				// Stop old client before creating new one to prevent goroutine leak
				slog.Debug("stopping old sprinkler client before recreation",
					"organization", organization)
				sprinklerClient.Stop()

				// Create new client with fresh token and fresh config
				newClient, err := client.New(createClientConfig(freshToken))
				if err != nil {
					slog.Error("failed to create sprinkler client after token refresh",
						"organization", organization,
						"error", err,
						"will_retry", true)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(retryDelay):
						continue
					}
				}

				sprinklerClient = newClient
				// Reset delay on successful token refresh and client recreation
				retryDelay = 5 * time.Second
				consecutiveErrors = 0
				slog.Info("successfully refreshed token and recreated sprinkler client",
					"organization", organization)
				continue
			}

			// Other error - log and retry with exponential backoff
			slog.Warn("sprinkler client stopped with error, will restart",
				"organization", organization,
				"error", startErr,
				"error_type", fmt.Sprintf("%T", startErr),
				"delay_seconds", retryDelay.Seconds(),
				"consecutive_errors", consecutiveErrors,
				"total_attempts", connectionAttempts)

			select {
			case <-ctx.Done():
				slog.Info("context cancelled during retry wait", "organization", organization)
				return ctx.Err()
			case <-time.After(retryDelay):
				// Exponential backoff capped at maxRetryDelay
				retryDelay *= 2
				if retryDelay > maxRetryDelay {
					retryDelay = maxRetryDelay
				}
				slog.Info("restarting sprinkler client after backoff",
					"organization", organization,
					"next_delay_seconds", retryDelay.Seconds(),
					"next_attempt", connectionAttempts+1)
				continue
			}
		}

		// Start() returned nil without error - this can happen on graceful disconnect
		// Check if context is still active before treating this as unexpected
		if ctxErr := ctx.Err(); ctxErr != nil {
			slog.Info("sprinkler client stopped cleanly due to context cancellation",
				"organization", organization)
			return ctxErr
		}

		// Unexpected clean return - log details and restart with minimal delay
		slog.Warn("sprinkler client Start() returned nil without error (unexpected clean disconnect)",
			"organization", organization,
			"total_attempts", connectionAttempts,
			"last_error", lastError,
			"time_since_last_error", time.Since(lastErrorTime),
			"consecutive_errors", consecutiveErrors,
			"will_restart_after_short_delay", true)

		// Use shorter delay for unexpected clean disconnects (not auth errors)
		// This might be network hiccup or server restart
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			slog.Info("restarting after unexpected clean disconnect",
				"organization", organization)
			continue
		}
	}
}
