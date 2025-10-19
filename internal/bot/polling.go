package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/github"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

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

	gqlClient := github.NewGraphQLClient(ctx, token)

	// Query all open PRs updated in last 24 hours
	prs, err := gqlClient.ListOpenPRs(ctx, org, 24)
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

	// Process each PR
	successCount := 0
	errorCount := 0

	for i := range prs {
		pr := &prs[i]

		// Create event key for this PR update to prevent duplicate processing
		eventKey := fmt.Sprintf("poll:%s:%s", pr.URL, pr.UpdatedAt.Format(time.RFC3339))

		// Skip if already processed (by webhook or previous poll)
		if c.stateStore.WasProcessed(eventKey) {
			slog.Debug("skipping PR - already processed",
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"pr_updated", pr.UpdatedAt)
			successCount++ // Count as success (already handled)
			continue
		}

		// Check if we need to notify about this PR
		if err := c.reconcilePR(ctx, pr); err != nil {
			slog.Warn("failed to reconcile PR",
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"error", err)
			errorCount++
		} else {
			// Mark as processed
			if err := c.stateStore.MarkProcessed(eventKey, 24*time.Hour); err != nil {
				slog.Warn("failed to mark poll event as processed",
					"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
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

	slog.Info("poll cycle complete",
		"org", org,
		"total_prs", len(prs),
		"processed", successCount,
		"errors", errorCount,
		"next_poll", "5m")
}

// reconcilePR checks a single PR and sends notifications if needed.
// This is called both from polling and startup reconciliation.
func (c *Coordinator) reconcilePR(ctx context.Context, pr *github.PRSnapshot) error {
	slog.Debug("reconciling PR",
		"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
		"updated_at", pr.UpdatedAt)

	// Get GitHub token for turnclient
	token := c.github.InstallationToken(ctx)
	if token == "" {
		return errors.New("no GitHub token available")
	}

	// Create turnclient to analyze PR state
	turnClient, err := turn.NewDefaultClient()
	if err != nil {
		return fmt.Errorf("failed to create turnclient: %w", err)
	}
	turnClient.SetAuthToken(token)

	// Check PR state with turnclient
	prURL := pr.URL
	checkCtx, checkCancel := context.WithTimeout(ctx, 30*time.Second)
	defer checkCancel()

	checkResult, err := turnClient.Check(checkCtx, prURL, pr.Owner, pr.UpdatedAt)
	if err != nil {
		return fmt.Errorf("turnclient check failed: %w", err)
	}

	slog.Debug("turnclient analysis complete",
		"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
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
			HTMLURL:   prURL,
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
		"window", "24h")

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
		eventKey := fmt.Sprintf("reconcile:%s:%s", pr.URL, pr.UpdatedAt.Format(time.RFC3339))

		// Check if we already processed this exact PR update (via webhook or previous reconciliation)
		if c.stateStore.WasProcessed(eventKey) {
			skippedCount++
			slog.Debug("skipping PR - already processed this update",
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"pr_updated", pr.UpdatedAt,
				"event_key", eventKey)
			continue
		}

		// Check notification state
		lastNotified := c.stateStore.GetLastNotification(pr.URL)

		// Determine if we should notify
		var reason string
		if lastNotified.IsZero() {
			reason = "never_notified"
		} else if pr.UpdatedAt.After(lastNotified) {
			reason = "updated_since_last_notification"
		} else {
			skippedCount++
			slog.Debug("skipping PR - already notified and not updated",
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"last_notified", lastNotified,
				"pr_updated", pr.UpdatedAt)
			continue
		}

		slog.Info("startup reconciliation - processing PR",
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"reason", reason,
			"last_notified", lastNotified,
			"pr_updated", pr.UpdatedAt)

		// Process this PR
		if err := c.reconcilePR(ctx, pr); err != nil {
			slog.Warn("startup reconciliation - failed to process PR",
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"error", err)
			errorCount++
		} else {
			reconciledCount++
			// Mark as processed to prevent duplicate processing
			if err := c.stateStore.MarkProcessed(eventKey, 24*time.Hour); err != nil {
				slog.Warn("failed to mark reconciliation event as processed",
					"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
					"error", err)
			}
			// Record that we notified
			if err := c.stateStore.RecordNotification(pr.URL, time.Now()); err != nil {
				slog.Warn("failed to record notification",
					"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
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
