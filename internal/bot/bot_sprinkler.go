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

// parsePRNumberFromURL extracts the PR number from a GitHub URL.
func parsePRNumberFromURL(url string) (int, error) {
	parts := strings.Split(url, "/")
	if len(parts) < githubURLMinParts || parts[2] != "github.com" || parts[5] != "pull" {
		return 0, fmt.Errorf("invalid GitHub URL format: %s (expected https://github.com/owner/repo/pull/123)", url)
	}

	num, err := strconv.Atoi(parts[6])
	if err != nil {
		return 0, fmt.Errorf("failed to parse PR number: %w", err)
	}

	if num == 0 {
		return 0, errors.New("invalid PR number (zero)")
	}

	return num, nil
}

// handleSprinklerEvent processes a single event from sprinkler.
func (c *Coordinator) handleSprinklerEvent(ctx context.Context, event client.Event, organization string) {
	// Deduplicate events using delivery_id if available, otherwise fall back to timestamp + URL + type
	// delivery_id is unique per GitHub webhook and is the same across all instances receiving the event
	var eventKey string
	if event.Raw != nil {
		if deliveryID, ok := event.Raw["delivery_id"].(string); ok && deliveryID != "" {
			eventKey = deliveryID
		}
	}
	if eventKey == "" {
		// Fallback to timestamp-based key if delivery_id not available
		eventKey = fmt.Sprintf("%s:%s:%s", event.Timestamp.Format(time.RFC3339Nano), event.URL, event.Type)
	}

	// Check persistent state first (survives restarts)
	if c.stateStore.WasProcessed(eventKey) {
		slog.Info("skipping duplicate event (persistent check)",
			"organization", organization,
			"type", event.Type,
			"url", event.URL,
			"timestamp", event.Timestamp,
			"event_key", eventKey)
		return
	}

	// Check if this event is currently being processed (prevents concurrent duplicates)
	// This is critical when sprinkler delivers the same event twice in quick succession
	c.processingEventMu.Lock()
	if c.processingEvents[eventKey] {
		c.processingEventMu.Unlock()
		slog.Info("skipping duplicate event (currently processing)",
			"organization", organization,
			"type", event.Type,
			"url", event.URL,
			"timestamp", event.Timestamp,
			"event_key", eventKey)
		return
	}
	// Mark as currently processing
	c.processingEvents[eventKey] = true
	c.processingEventMu.Unlock()

	// Ensure we clean up the processing flag when done
	defer func() {
		c.processingEventMu.Lock()
		delete(c.processingEvents, eventKey)
		c.processingEventMu.Unlock()
	}()

	// Also check in-memory for fast deduplication during normal operation
	c.processedEventMu.Lock()
	if processedTime, exists := c.processedEvents[eventKey]; exists {
		c.processedEventMu.Unlock()
		slog.Info("skipping duplicate event (memory check)",
			"organization", organization,
			"type", event.Type,
			"url", event.URL,
			"timestamp", event.Timestamp,
			"first_processed", processedTime,
			"event_key", eventKey)
		return
	}
	c.processedEvents[eventKey] = time.Now()

	// Cleanup old in-memory events (older than 1 hour - persistent store handles long-term)
	cutoff := time.Now().Add(-1 * time.Hour)
	cleanedCount := 0
	for key, processedTime := range c.processedEvents {
		if processedTime.Before(cutoff) {
			delete(c.processedEvents, key)
			cleanedCount++
		}
	}
	if cleanedCount > 0 {
		slog.Debug("cleaned up old in-memory processed events",
			"organization", organization,
			"removed_count", cleanedCount,
			"remaining_count", len(c.processedEvents))
	}
	c.processedEventMu.Unlock()

	slog.Info("processing sprinkler event",
		"organization", organization,
		"type", event.Type,
		"url", event.URL,
		"timestamp", event.Timestamp,
		"event_key", eventKey)

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

	prNumber, err := parsePRNumberFromURL(event.URL)
	if err != nil {
		slog.Error("failed to parse PR number from URL",
			"organization", organization,
			"url", event.URL,
			"error", err)
		return
	}

	slog.Debug("extracted PR number from URL",
		"organization", organization,
		"pr_number", prNumber,
		"url", event.URL)

	// Extract owner/repo from URL
	parts := strings.Split(event.URL, "/")
	if len(parts) < 5 || parts[2] != "github.com" {
		slog.Error("could not extract repo from URL",
			"organization", organization,
			"url", event.URL,
			"error", "invalid URL format")
		return
	}
	repo := parts[3] + "/" + parts[4]

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
			"organization", organization,
			"error", err,
			"type", event.Type,
			"url", event.URL,
			"repo", repo)
		// Don't mark as processed if processing failed - allow retry
		return
	}

	// Mark event as processed in persistent state (survives restarts)
	if err := c.stateStore.MarkProcessed(eventKey, 24*time.Hour); err != nil {
		slog.Warn("failed to mark event as processed",
			"organization", organization,
			"event_key", eventKey,
			"error", err)
		// Continue anyway - in-memory dedup will prevent immediate duplicates
	}
}

// handleAuthError handles authentication errors by refreshing the token and recreating the client.
func (c *Coordinator) handleAuthError(
	ctx context.Context,
	organization string,
	createConfig func(context.Context, string) client.Config,
) (*client.Client, error) {
	if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
		slog.Error("failed to refresh token",
			"organization", organization,
			"refresh_error", refreshErr)
		return nil, refreshErr
	}

	freshToken := c.github.InstallationToken(ctx)
	if freshToken == "" {
		return nil, errors.New("token refresh succeeded but returned empty token")
	}

	slog.Debug("creating new sprinkler client with refreshed token", "organization", organization)
	newClient, err := client.New(createConfig(ctx, freshToken))
	if err != nil {
		return nil, fmt.Errorf("failed to create sprinkler client after token refresh: %w", err)
	}

	slog.Info("successfully refreshed token and recreated sprinkler client", "organization", organization)
	return newClient, nil
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

	// createClientConfig creates a new sprinkler client config with fresh token.
	// This is a helper to avoid duplicating config setup.
	createClientConfig := func(ctx context.Context, token string) client.Config {
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
				// Use background context for event processing to avoid losing events during shutdown.
				// Note: No panic recovery - we want panics to propagate and restart the service.
				eventCtx := context.WithoutCancel(ctx)
				c.handleSprinklerEvent(eventCtx, event, organization)
			},
		}
	}

	// Create the sprinkler client
	sprinklerClient, err := client.New(createClientConfig(ctx, githubToken))
	if err != nil {
		return fmt.Errorf("failed to create sprinkler client: %w", err)
	}

	// Start the client (this will handle reconnection, ping/pong, etc.)
	// Note: Start() may return when connection is lost, so we loop to restart it
	slog.Info("starting sprinkler client", "organization", organization)

	// Start cleanup ticker for thread cache
	// Clean up threads older than 30 days to prevent unbounded growth
	cleanupTicker := time.NewTicker(6 * time.Hour)
	defer cleanupTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-cleanupTicker.C:
				c.threadCache.Cleanup(30 * 24 * time.Hour)
				slog.Debug("cleaned up old thread cache entries", "organization", organization)
			}
		}
	}()

	retryDelay := 5 * time.Second
	maxRetryDelay := 60 * time.Second
	errCount := 0
	attempts := 0
	var lastError error
	var lastErrorTime time.Time

	for {
		attempts++
		slog.Info("attempting sprinkler connection",
			"organization", organization,
			"attempt", attempts,
			"consecutive_errors", errCount,
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
				"total_attempts", attempts)
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
			errCount++

			// Check if it's an authentication error
			if strings.Contains(startErr.Error(), "403") || strings.Contains(startErr.Error(), "401") {
				slog.Warn("authentication failed, refreshing token",
					"organization", organization,
					"consecutive_errors", errCount,
					"error", startErr)

				sprinklerClient.Stop() // Stop old client before creating new one

				newClient, err := c.handleAuthError(ctx, organization, createClientConfig)
				if err != nil {
					slog.Error("failed to handle auth error",
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
				retryDelay = 5 * time.Second
				errCount = 0
				continue
			}

			// Other error - log and retry with exponential backoff
			slog.Warn("sprinkler client stopped with error, will restart",
				"organization", organization,
				"error", startErr,
				"error_type", fmt.Sprintf("%T", startErr),
				"delay_seconds", retryDelay.Seconds(),
				"consecutive_errors", errCount,
				"total_attempts", attempts)

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
					"next_attempt", attempts+1)
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
			"total_attempts", attempts,
			"last_error", lastError,
			"time_since_last_error", time.Since(lastErrorTime),
			"consecutive_errors", errCount,
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
