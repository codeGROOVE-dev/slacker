package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
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

// eventKey generates a unique key for event deduplication.
// Uses delivery_id if available (GitHub's unique webhook ID),
// otherwise falls back to timestamp + URL + type.
func eventKey(event client.Event) string {
	if event.Raw != nil {
		if id, ok := event.Raw["delivery_id"].(string); ok && id != "" {
			return id
		}
	}
	return fmt.Sprintf("%s:%s:%s", event.Timestamp.Format(time.RFC3339Nano), event.URL, event.Type)
}

// lookupPRsForCheckEvent looks up PRs for a check event that only has a repo URL.
// This happens when sprinkler's cache doesn't know about the PR yet (timing race).
func (c *Coordinator) lookupPRsForCheckEvent(ctx context.Context, event client.Event, organization string) []int {
	commitSHA := ""
	deliveryID := ""
	if event.Raw != nil {
		if sha, ok := event.Raw["commit_sha"].(string); ok {
			commitSHA = sha
		}
		if id, ok := event.Raw["delivery_id"].(string); ok {
			deliveryID = id
		}
	}

	if commitSHA == "" {
		slog.Warn("check event without PR number or commit SHA - cannot look up PRs",
			"organization", organization,
			"url", event.URL,
			"type", event.Type,
			"delivery_id", deliveryID)
		return nil
	}

	// Extract owner/repo from URL
	parts := strings.Split(event.URL, "/")
	if len(parts) < 5 || parts[2] != "github.com" {
		slog.Error("could not extract repo from check event URL",
			"organization", organization,
			"url", event.URL,
			"error", "invalid URL format")
		return nil
	}
	owner := parts[3]
	repo := parts[4]

	// First, check our local commit→PR cache (populated from recent PR events)
	// This is MUCH faster than GitHub API and usually works since check events
	// arrive seconds after PR events
	cachedPRs := c.commitPRCache.FindPRsForCommit(owner, repo, commitSHA)
	if len(cachedPRs) > 0 {
		slog.Info("found PRs for commit in cache - avoiding GitHub API call",
			"organization", organization,
			"owner", owner,
			"repo", repo,
			"commit_sha", commitSHA,
			"pr_count", len(cachedPRs),
			"pr_numbers", cachedPRs,
			"type", event.Type,
			"delivery_id", deliveryID,
			"cache_hit", true)
		return cachedPRs
	}

	// Cache miss - try turnclient lookup on most recent PR before falling back to GitHub API
	slog.Info("commit→PR cache miss - will try turnclient on recent PR before GitHub API",
		"organization", organization,
		"owner", owner,
		"repo", repo,
		"commit_sha", commitSHA,
		"type", event.Type,
		"delivery_id", deliveryID,
		"cache_hit", false,
		"reason", "check event arrived before PR event or cache expired")

	// Second attempt: Check if we recently saw a PR for this repo
	// If yes, fetch it via turnclient to see if it contains this commit
	// This is cheaper than searching all PRs via GitHub API
	mostRecentPR := c.commitPRCache.MostRecentPR(owner, repo)
	//nolint:nestif // Complex but necessary cache population logic with early returns
	if mostRecentPR > 0 {
		slog.Debug("attempting turnclient lookup on most recent PR for repo",
			"organization", organization,
			"owner", owner,
			"repo", repo,
			"pr_number", mostRecentPR,
			"commit_sha", commitSHA,
			"rationale", "check events usually arrive for recently updated PRs")

		// Get GitHub token
		githubToken := c.github.InstallationToken(ctx)
		if githubToken != "" {
			// Create turnclient (allow test backend override for mocking)
			var turnClient *turn.Client
			var tcErr error
			if testBackend := os.Getenv("TURN_TEST_BACKEND"); testBackend != "" {
				turnClient, tcErr = turn.NewClient(testBackend)
			} else {
				turnClient, tcErr = turn.NewDefaultClient()
			}
			if tcErr == nil {
				turnClient.SetAuthToken(githubToken)

				// Check the recent PR with current timestamp
				checkCtx, checkCancel := context.WithTimeout(ctx, 30*time.Second)
				prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, mostRecentPR)
				checkResult, checkErr := turnClient.Check(checkCtx, prURL, owner, time.Now())
				checkCancel()

				if checkErr == nil && checkResult != nil {
					// Check if any commits match
					for _, prCommit := range checkResult.PullRequest.Commits {
						if prCommit == commitSHA {
							slog.Info("found commit in most recent PR via turnclient - avoiding GitHub API search",
								"organization", organization,
								"owner", owner,
								"repo", repo,
								"pr_number", mostRecentPR,
								"commit_sha", commitSHA,
								"turnclient_hit", true,
								"bonus", "got free PR status update")

							// Populate cache with all commits from this PR
							for _, commit := range checkResult.PullRequest.Commits {
								//nolint:revive // Nesting depth acceptable for cache population logic
								if commit != "" {
									c.commitPRCache.RecordPR(owner, repo, mostRecentPR, commit)
								}
							}

							// Process the PR update since we have fresh data
							c.handlePullRequestEventWithData(ctx, owner, repo, struct {
								Action      string `json:"action"`
								PullRequest struct {
									HTMLURL   string    `json:"html_url"`
									Title     string    `json:"title"`
									CreatedAt time.Time `json:"created_at"`
									User      struct {
										Login string `json:"login"`
									} `json:"user"`
									Number int `json:"number"`
								} `json:"pull_request"`
								Number int `json:"number"`
							}{
								Action: "synchronize",
								PullRequest: struct {
									HTMLURL   string    `json:"html_url"`
									Title     string    `json:"title"`
									CreatedAt time.Time `json:"created_at"`
									User      struct {
										Login string `json:"login"`
									} `json:"user"`
									Number int `json:"number"`
								}{
									HTMLURL:   prURL,
									Title:     checkResult.PullRequest.Title,
									CreatedAt: checkResult.PullRequest.CreatedAt,
									User: struct {
										Login string `json:"login"`
									}{
										Login: checkResult.PullRequest.Author,
									},
									Number: mostRecentPR,
								},
								Number: mostRecentPR,
							}, checkResult, nil)

							return []int{mostRecentPR}
						}
					}

					slog.Debug("commit not found in most recent PR - will fall back to GitHub API",
						"organization", organization,
						"owner", owner,
						"repo", repo,
						"pr_number", mostRecentPR,
						"commit_sha", commitSHA,
						"pr_commits_checked", len(checkResult.PullRequest.Commits))
				}
			}
		}
	}

	// Third attempt: Fall back to GitHub API to search all PRs
	slog.Info("falling back to GitHub API to search all PRs for commit",
		"organization", organization,
		"owner", owner,
		"repo", repo,
		"commit_sha", commitSHA,
		"tried_turnclient", mostRecentPR > 0)

	// Look up PRs for this commit using GitHub API
	// Allow up to 3 minutes for retries (2 min max delay + buffer)
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	prNumbers, lookupErr := c.github.FindPRsForCommit(lookupCtx, owner, repo, commitSHA)
	if lookupErr != nil {
		slog.Error("failed to look up PRs for commit",
			"organization", organization,
			"owner", owner,
			"repo", repo,
			"commit_sha", commitSHA,
			"error", lookupErr,
			"impact", "will miss this check event",
			"fallback", "5-minute polling will catch it")
		return nil
	}

	if len(prNumbers) == 0 {
		slog.Debug("no open PRs found for commit - likely push to main",
			"organization", organization,
			"owner", owner,
			"repo", repo,
			"commit_sha", commitSHA,
			"type", event.Type)
		return nil
	}

	slog.Info("found PRs for commit - processing check event",
		"organization", organization,
		"owner", owner,
		"repo", repo,
		"commit_sha", commitSHA,
		"pr_count", len(prNumbers),
		"pr_numbers", prNumbers)

	return prNumbers
}

// handleSprinklerEvent processes a single event from sprinkler.
func (c *Coordinator) handleSprinklerEvent(ctx context.Context, event client.Event, organization string) {
	// Generate event key using delivery_id if available, otherwise timestamp + URL + type.
	// delivery_id is unique per GitHub webhook and is the same across all instances.
	eventKey := eventKey(event)

	// Try to claim this event atomically using persistent store (Datastore transaction).
	// This is the single source of truth for cross-instance deduplication.
	if err := c.stateStore.MarkProcessed(ctx, eventKey, 24*time.Hour); err != nil {
		// Check if this is a race condition vs a database error
		if errors.Is(err, state.ErrAlreadyProcessed) {
			slog.Info("skipping duplicate event - claimed by this or another instance",
				"organization", organization,
				"type", event.Type,
				"url", event.URL,
				"event_key", eventKey,
				"reason", "deduplication_race")
		} else {
			slog.Error("failed to mark event as processed - database error",
				"organization", organization,
				"type", event.Type,
				"url", event.URL,
				"event_key", eventKey,
				"error", err,
				"impact", "event_dropped_will_retry_via_polling",
				"next_poll_in", "5m")
		}
		return
	}

	slog.Info("accepted event for async processing",
		"organization", organization,
		"type", event.Type,
		"url", event.URL,
		"timestamp", event.Timestamp,
		"event_key", eventKey)

	// Process event asynchronously after deduplication checks pass
	// This allows the event handler to return immediately and accept the next event
	// Semaphore limits concurrent processing to prevent overwhelming APIs
	// WaitGroup tracks in-flight events for graceful shutdown
	c.processingEvents.Add(1)
	go func() {
		defer c.processingEvents.Done()

		// Acquire semaphore slot (blocks if 10 events already processing)
		c.eventSemaphore <- struct{}{}
		defer func() { <-c.eventSemaphore }() // Release slot when done

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
			// Sprinkler sends events with just repo URLs when it can't find PRs for a commit
			// For check events, try to recover by looking up PRs ourselves
			if event.Type == "check_suite" || event.Type == "check_run" {
				prNumbers := c.lookupPRsForCheckEvent(ctx, event, organization)
				if prNumbers == nil {
					return
				}

				// Extract owner/repo from URL for constructing PR URLs
				parts := strings.Split(event.URL, "/")
				owner := parts[3]
				repo := parts[4]

				// Process event for each PR found
				for _, num := range prNumbers {
					msg := SprinklerMessage{
						Type:      event.Type,
						Event:     event.Type,
						Repo:      owner + "/" + repo,
						PRNumber:  num,
						URL:       fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, num),
						Timestamp: event.Timestamp,
					}

					// Process with timeout
					processCtx, processCancel := context.WithTimeout(ctx, 30*time.Second)
					if err := c.processEvent(processCtx, msg); err != nil {
						slog.Error("error processing recovered check event",
							"organization", organization,
							"error", err,
							"type", event.Type,
							"url", msg.URL,
							"repo", msg.Repo,
							"pr_number", num)
					}
					processCancel()
				}
				return
			}

			// Non-check events without PR numbers - just log and ignore
			var commitSHA, deliveryID string
			if event.Raw != nil {
				if sha, ok := event.Raw["commit_sha"].(string); ok {
					commitSHA = sha
				}
				if id, ok := event.Raw["delivery_id"].(string); ok {
					deliveryID = id
				}
			}
			slog.Debug("ignoring event without PR number",
				"organization", organization,
				"url", event.URL,
				"type", event.Type,
				"commit_sha", commitSHA,
				"delivery_id", deliveryID)
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

		// Add timeout to prevent hanging on external API failures
		processCtx, processCancel := context.WithTimeout(ctx, 30*time.Second)
		defer processCancel()

		if err := c.processEvent(processCtx, msg); err != nil {
			timedOut := errors.Is(err, context.DeadlineExceeded)
			slog.Error("error processing event",
				"organization", organization,
				"error", err,
				"type", event.Type,
				"url", event.URL,
				"repo", repo,
				"timed_out", timedOut,
				"impact", "event_dropped_will_retry_via_polling",
				"next_poll_in", "5m")
			// Event already marked as processed before goroutine started.
			// Failed processing won't be retried automatically.
			// Polling will catch this within 5 minutes for open PRs.
			return
		}

		slog.Info("successfully processed sprinkler event",
			"organization", organization,
			"type", event.Type,
			"url", event.URL,
			"event_key", eventKey)
	}() // Close the goroutine
}

// waitForEventProcessing waits for all in-flight events to complete during shutdown.
// Returns immediately if no events are being processed.
func (c *Coordinator) waitForEventProcessing(organization string, maxWait time.Duration) {
	// Check if any events are being processed
	queueLen := len(c.eventSemaphore)
	if queueLen == 0 {
		slog.Info("no events in processing queue, shutdown can proceed immediately",
			"organization", organization)
		return
	}

	slog.Warn("waiting for in-flight events to complete before shutdown",
		"organization", organization,
		"events_in_queue", queueLen,
		"max_wait_seconds", maxWait.Seconds())

	// Create a channel to signal when all events are done
	done := make(chan struct{})
	go func() {
		c.processingEvents.Wait()
		close(done)
	}()

	// Wait for events to complete or timeout
	select {
	case <-done:
		slog.Info("all in-flight events completed successfully",
			"organization", organization,
			"graceful_shutdown", true)
	case <-time.After(maxWait):
		remaining := len(c.eventSemaphore)
		slog.Warn("shutdown timeout reached, proceeding with remaining events in queue",
			"organization", organization,
			"events_still_processing", remaining,
			"waited_seconds", maxWait.Seconds(),
			"impact", "these events may be incomplete",
			"recovery", "polling will catch them in next 5min cycle",
			"graceful_shutdown", false)
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
//
//nolint:revive,maintidx // Complex retry/reconnection logic requires length for robustness
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
				slog.Warn("🟢 SPRINKLER CONNECTED",
					"organization", organization,
					"url", c.sprinklerURL,
					"subscribed_events", "*",
					"critical", "now receiving real-time webhook events")
			},
			OnDisconnect: func(err error) {
				if err != nil {
					slog.Error("🔴 SPRINKLER DISCONNECTED - WILL MISS EVENTS UNTIL RECONNECTED",
						"organization", organization,
						"error", err,
						"impact", "real-time webhook events will be missed",
						"fallback", "5-minute polling will catch missed events",
						"action_required", "investigate why connection dropped")
					return
				}
				slog.Warn("🟡 SPRINKLER DISCONNECTED (graceful)",
					"organization", organization,
					"reason", "clean shutdown or reconnection attempt",
					"impact", "may miss events during reconnection window")
			},
			OnEvent: func(event client.Event) {
				// SECURITY NOTE: Use detached context for event processing to prevent webhook
				// events from being lost during shutdown. Event processing has internal timeouts
				// (30s for turnclient, semaphore limits) to prevent resource exhaustion.
				// This ensures all GitHub events are processed reliably while maintaining security.
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

	// Connection health monitoring - log every minute to detect silent disconnections
	healthTicker := time.NewTicker(1 * time.Minute)
	defer healthTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-cleanupTicker.C:
				c.threadCache.Cleanup(30 * 24 * time.Hour)
				slog.Debug("cleaned up old thread cache entries", "organization", organization)
			case <-healthTicker.C:
				// Log connection health every minute
				// This helps detect if we're silently disconnected for long periods
				slog.Debug("sprinkler connection health check",
					"organization", organization,
					"note", "if you see this log but no events for >2min during active PR work, connection may be broken")
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
			// Wait for in-flight events (up to 8 seconds, leaving 2s for HTTP shutdown)
			c.waitForEventProcessing(organization, 8*time.Second)
			return nil
		}

		// Check if context was cancelled even without error
		// (this handles the case where Start() returns nil on clean shutdown)
		if ctxErr := ctx.Err(); ctxErr != nil {
			slog.Info("context cancelled, stopping sprinkler client",
				"organization", organization,
				"context_error", ctxErr)
			// Wait for in-flight events (up to 8 seconds)
			c.waitForEventProcessing(organization, 8*time.Second)
			return ctxErr
		}

		// Handle different error types
		if startErr != nil {
			errCount++

			// Check if it's an authentication error OR if we've had many failures (token might be expired)
			// After 5 consecutive failures, proactively refresh the token
			isAuthError := strings.Contains(startErr.Error(), "403") || strings.Contains(startErr.Error(), "401")
			shouldRefreshToken := isAuthError || errCount >= 5

			if shouldRefreshToken {
				if isAuthError {
					slog.Warn("authentication failed, refreshing token",
						"organization", organization,
						"consecutive_errors", errCount,
						"error", startErr)
				} else {
					slog.Info("multiple connection failures, proactively refreshing token",
						"organization", organization,
						"consecutive_errors", errCount,
						"error", startErr,
						"reason", "token may have expired")
				}

				sprinklerClient.Stop() // Stop old client before creating new one

				newClient, err := c.handleAuthError(ctx, organization, createClientConfig)
				if err != nil {
					slog.Error("failed to handle auth error",
						"organization", organization,
						"error", err,
						"will_retry", true)
					select {
					case <-ctx.Done():
						c.waitForEventProcessing(organization, 8*time.Second)
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
				c.waitForEventProcessing(organization, 8*time.Second)
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
			c.waitForEventProcessing(organization, 8*time.Second)
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
			c.waitForEventProcessing(organization, 8*time.Second)
			return ctx.Err()
		case <-time.After(5 * time.Second):
			slog.Info("restarting after unexpected clean disconnect",
				"organization", organization)
			continue
		}
	}
}
