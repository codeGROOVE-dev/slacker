package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// makePollEventKey creates an event key for poll-based PR processing.
// This is a pure function that can be easily tested.
func makePollEventKey(prURL string, updatedAt time.Time) string {
	return fmt.Sprintf("poll:%s:%s", prURL, updatedAt.Format(time.RFC3339))
}

// makeClosedPREventKey creates an event key for closed/merged PR updates.
// This is a pure function that can be easily tested.
func makeClosedPREventKey(prURL, state string, updatedAt time.Time) string {
	return fmt.Sprintf("poll_closed:%s:%s:%s", prURL, state, updatedAt.Format(time.RFC3339))
}

// formatPRIdentifier creates a human-readable PR identifier.
// This is a pure function that can be easily tested.
func formatPRIdentifier(owner, repo string, prNumber int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)
}

// makeReconcileEventKey creates an event key for startup reconciliation.
// This is a pure function that can be easily tested.
func makeReconcileEventKey(prURL string, updatedAt time.Time) string {
	return fmt.Sprintf("reconcile:%s:%s", prURL, updatedAt.Format(time.RFC3339))
}

// PollAndReconcile checks all open PRs and ensures notifications are sent.
// This runs every 5 minutes as a safety net to catch anything sprinkler missed.
func (c *Coordinator) PollAndReconcile(ctx context.Context) {
	org := c.github.Organization()
	if org == "" {
		slog.Warn("polling skipped - no organization configured")
		return
	}

	slog.Info("starting periodic PR poll",
		"org", org,
		"interval", "5m",
		"window", "24h")

	// Create GraphQL client with current token
	token := c.github.InstallationToken(ctx)
	if token == "" {
		slog.Warn("polling skipped - no GitHub token available", "org", org)
		return
	}

	searcher := github.NewGraphQLClient(ctx, token)
	c.pollAndReconcileWithSearcher(ctx, searcher, org)
}

// pollAndReconcileWithSearcher is the core polling logic, extracted for testability.
func (c *Coordinator) pollAndReconcileWithSearcher(ctx context.Context, searcher PRSearcher, org string) {
	// Query all open PRs updated in last 24 hours
	prs, err := searcher.ListOpenPRs(ctx, org, 24)
	if err != nil {
		slog.Error("failed to poll open PRs",
			"org", org,
			"error", err,
			"next_poll", "5m")
		return
	}

	slog.Info("poll retrieved PRs",
		"org", org,
		"pr_count", len(prs),
		"will_check_each", true)

	// Process each open PR
	successCount := 0
	errorCount := 0

	for i := range prs {
		pr := &prs[i]

		// Create event key for this PR update to prevent duplicate processing
		eventKey := makePollEventKey(pr.URL, pr.UpdatedAt)

		// Skip if already processed (by webhook or previous poll)
		if c.stateStore.WasProcessed(ctx, eventKey) {
			slog.Debug("skipping PR - already processed",
				"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
				"pr_updated", pr.UpdatedAt)
			successCount++ // Count as success (already handled)
			continue
		}

		// Check if we need to notify about this PR
		if err := c.reconcilePR(ctx, pr); err != nil {
			slog.Warn("failed to reconcile PR",
				"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
				"error", err)
			errorCount++
		} else {
			// Mark as processed
			if err := c.stateStore.MarkProcessed(ctx, eventKey, 24*time.Hour); err != nil {
				slog.Warn("failed to mark poll event as processed",
					"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
					"error", err)
			}
			successCount++
		}

		// Rate limit: Small delay between PRs to avoid hammering GitHub API
		select {
		case <-ctx.Done():
			slog.Info("polling canceled", "org", org)
			return
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Query closed/merged PRs in last hour to update existing threads
	closedPRs, err := searcher.ListClosedPRs(ctx, org, 1)
	if err != nil {
		slog.Warn("failed to poll closed PRs",
			"org", org,
			"error", err,
			"impact", "will retry next poll")
	} else {
		slog.Info("poll retrieved closed/merged PRs",
			"org", org,
			"pr_count", len(closedPRs),
			"will_update_threads", true)

		closedSuccessCount := 0
		closedErrorCount := 0

		for i := range closedPRs {
			pr := &closedPRs[i]

			// Create event key for this PR state change
			eventKey := makeClosedPREventKey(pr.URL, pr.State, pr.UpdatedAt)

			// Skip if already processed
			if c.stateStore.WasProcessed(ctx, eventKey) {
				slog.Debug("skipping closed PR - already processed",
					"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
					"state", pr.State)
				closedSuccessCount++
				continue
			}

			// Update thread for this closed/merged PR
			if err := c.updateClosedPRThread(ctx, pr); err != nil {
				slog.Warn("failed to update closed PR thread",
					"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
					"state", pr.State,
					"error", err)
				closedErrorCount++
			} else {
				// Mark as processed
				if err := c.stateStore.MarkProcessed(ctx, eventKey, 24*time.Hour); err != nil {
					slog.Warn("failed to mark closed PR event as processed",
						"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
						"error", err)
				}
				closedSuccessCount++
			}

			// Rate limit
			select {
			case <-ctx.Done():
				slog.Info("polling canceled during closed PR processing", "org", org)
				return
			case <-time.After(100 * time.Millisecond):
			}
		}

		slog.Info("closed PR processing complete",
			"org", org,
			"total_closed_prs", len(closedPRs),
			"updated", closedSuccessCount,
			"errors", closedErrorCount)
	}

	slog.Info("poll cycle complete",
		"org", org,
		"total_open_prs", len(prs),
		"processed", successCount,
		"errors", errorCount,
		"next_poll", "5m")
}

// reconcilePR checks a single PR and sends notifications if needed.
// This is called both from polling and startup reconciliation.
func (c *Coordinator) reconcilePR(ctx context.Context, pr *github.PRSnapshot) error {
	slog.Debug("reconciling PR",
		"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
		"updated_at", pr.UpdatedAt)

	// Get GitHub token for turnclient
	token := c.github.InstallationToken(ctx)
	if token == "" {
		return errors.New("no GitHub token available")
	}

	// Create turnclient to analyze PR state
	// Allow test backend override for mocking in tests
	var turnClient *turn.Client
	var err error
	if testBackend := os.Getenv("TURN_TEST_BACKEND"); testBackend != "" {
		turnClient, err = turn.NewClient(testBackend)
	} else {
		turnClient, err = turn.NewDefaultClient()
	}
	if err != nil {
		return fmt.Errorf("failed to create turnclient: %w", err)
	}
	turnClient.SetAuthToken(token)

	// Check PR state with turnclient
	checkCtx, checkCancel := context.WithTimeout(ctx, 30*time.Second)
	defer checkCancel()

	checkResult, err := turnClient.Check(checkCtx, pr.URL, pr.Owner, pr.UpdatedAt)
	if err != nil {
		return fmt.Errorf("turnclient check failed: %w", err)
	}

	slog.Debug("turnclient analysis complete",
		"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
		"pr_state", checkResult.PullRequest.State,
		"pr_draft", checkResult.PullRequest.Draft,
		"pr_merged", checkResult.PullRequest.Merged,
		"ready_to_merge", checkResult.Analysis.ReadyToMerge,
		"approved", checkResult.Analysis.Approved,
		"next_action_count", len(checkResult.Analysis.NextAction))

	// Create synthetic webhook event to reuse existing logic
	event := struct {
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
		Action: "synchronize", // Use synchronize for poll-based updates
		PullRequest: struct {
			HTMLURL   string    `json:"html_url"`
			Title     string    `json:"title"`
			CreatedAt time.Time `json:"created_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
			Number int `json:"number"`
		}{
			HTMLURL:   pr.URL,
			Title:     pr.Title,
			CreatedAt: pr.CreatedAt,
			User: struct {
				Login string `json:"login"`
			}{
				Login: pr.Author,
			},
			Number: pr.Number,
		},
		Number: pr.Number,
	}

	// Use existing event handler to process this PR
	c.handlePullRequestEventWithData(ctx, pr.Owner, pr.Repo, event, checkResult, nil)

	return nil
}

// isChannelResolutionFailed determines if channel ID resolution failed.
// Returns true if the resolved ID indicates failure (matches original name or is stripped hash).
func isChannelResolutionFailed(channelName, resolvedID string) bool {
	// If resolved ID matches the input channel name, resolution failed
	if resolvedID == channelName {
		return true
	}
	// If channel name starts with # and resolved ID is the name with # stripped, resolution failed
	if channelName != "" && channelName[0] == '#' && resolvedID == channelName[1:] {
		return true
	}
	return false
}

// updateClosedPRThread updates Slack threads for a closed or merged PR.
// Delegates to reconcilePR which handles turnclient checks and thread updates.
func (c *Coordinator) updateClosedPRThread(ctx context.Context, pr *github.PRSnapshot) error {
	// reconcilePR already does everything we need:
	// - Calls turnclient to distinguish merged vs closed-but-not-merged
	// - Updates all channel threads with correct emoji
	// - Sends DM updates if needed
	return c.reconcilePR(ctx, pr)
}

// shouldReconcilePR determines if a PR should be reconciled based on notification history.
// Returns (reason, shouldReconcile). This is a pure function that can be easily tested.
func shouldReconcilePR(prUpdatedAt, lastNotified time.Time) (string, bool) {
	switch {
	case lastNotified.IsZero():
		return "never_notified", true
	case prUpdatedAt.After(lastNotified):
		return "updated_since_last_notification", true
	default:
		return "already_notified", false
	}
}

// StartupReconciliation runs once at startup to catch up on any missed notifications.
// This ensures that if the service was down, we still notify about PRs that need attention.
func (c *Coordinator) StartupReconciliation(ctx context.Context) {
	org := c.github.Organization()
	if org == "" {
		slog.Warn("startup reconciliation skipped - no organization configured")
		return
	}

	slog.Info("🔄 STARTUP RECONCILIATION STARTED",
		"org", org,
		"purpose", "catch up on missed notifications during downtime",
		"window", "24h",
		"scope", "open_prs_only",
		"note", "closed PRs handled by polling cycle")

	// Get current GitHub token
	token := c.github.InstallationToken(ctx)
	if token == "" {
		slog.Warn("startup reconciliation skipped - no GitHub token available", "org", org)
		return
	}

	// Create GraphQL client
	gqlClient := github.NewGraphQLClient(ctx, token)

	// Query all open PRs updated in last 24 hours
	prs, err := gqlClient.ListOpenPRs(ctx, org, 24)
	if err != nil {
		slog.Error("startup reconciliation failed to query PRs",
			"org", org,
			"error", err)
		return
	}

	slog.Info("startup reconciliation - PRs retrieved",
		"org", org,
		"pr_count", len(prs),
		"will_check_notifications", true)

	// Check each PR and send notifications if needed
	reconciledCount := 0
	skippedCount := 0
	errorCount := 0

	for i := range prs {
		pr := &prs[i]

		// Create event key for this PR update (same format as webhook events)
		// This prevents processing the same update twice if a webhook was already received
		eventKey := makeReconcileEventKey(pr.URL, pr.UpdatedAt)

		// Check if we already processed this exact PR update (via webhook or previous reconciliation)
		if c.stateStore.WasProcessed(ctx, eventKey) {
			skippedCount++
			slog.Debug("skipping PR - already processed this update",
				"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
				"pr_updated", pr.UpdatedAt,
				"event_key", eventKey)
			continue
		}

		// Check notification state
		lastNotified := c.stateStore.LastNotification(ctx, pr.URL)

		// Determine if we should notify
		reason, shouldNotify := shouldReconcilePR(pr.UpdatedAt, lastNotified)
		if !shouldNotify {
			skippedCount++
			slog.Debug("skipping PR - already notified and not updated",
				"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
				"last_notified", lastNotified,
				"pr_updated", pr.UpdatedAt)
			continue
		}

		slog.Info("startup reconciliation - processing PR",
			"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
			"reason", reason,
			"last_notified", lastNotified,
			"pr_updated", pr.UpdatedAt)

		// Process this PR
		if err := c.reconcilePR(ctx, pr); err != nil {
			slog.Warn("startup reconciliation - failed to process PR",
				"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
				"error", err)
			errorCount++
		} else {
			reconciledCount++
			// Mark as processed to prevent duplicate processing
			if err := c.stateStore.MarkProcessed(ctx, eventKey, 24*time.Hour); err != nil {
				slog.Warn("failed to mark reconciliation event as processed",
					"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
					"error", err)
			}
			// Record that we notified
			if err := c.stateStore.RecordNotification(ctx, pr.URL, time.Now()); err != nil {
				slog.Warn("failed to record notification",
					"pr", formatPRIdentifier(pr.Owner, pr.Repo, pr.Number),
					"error", err)
			}
		}

		// Rate limit
		select {
		case <-ctx.Done():
			slog.Info("startup reconciliation canceled", "org", org)
			return
		case <-time.After(200 * time.Millisecond):
		}
	}

	slog.Info("✅ STARTUP RECONCILIATION COMPLETE",
		"org", org,
		"total_prs", len(prs),
		"reconciled", reconciledCount,
		"skipped", skippedCount,
		"errors", errorCount)
}
