package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/github"
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

	// Process each open PR
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

	// Query closed/merged PRs in last hour to update existing threads
	closedPRs, err := gqlClient.ListClosedPRs(ctx, org, 1)
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
			eventKey := fmt.Sprintf("poll_closed:%s:%s:%s", pr.URL, pr.State, pr.UpdatedAt.Format(time.RFC3339))

			// Skip if already processed
			if c.stateStore.WasProcessed(eventKey) {
				slog.Debug("skipping closed PR - already processed",
					"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
					"state", pr.State)
				closedSuccessCount++
				continue
			}

			// Update thread for this closed/merged PR
			if err := c.updateClosedPRThread(ctx, pr); err != nil {
				slog.Warn("failed to update closed PR thread",
					"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
					"state", pr.State,
					"error", err)
				closedErrorCount++
			} else {
				// Mark as processed
				if err := c.stateStore.MarkProcessed(eventKey, 24*time.Hour); err != nil {
					slog.Warn("failed to mark closed PR event as processed",
						"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
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

// updateClosedPRThread updates Slack threads for a closed or merged PR.
func (c *Coordinator) updateClosedPRThread(ctx context.Context, pr *github.PRSnapshot) error {
	prKey := fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number)
	slog.Debug("updating thread for closed/merged PR",
		"pr", prKey,
		"state", pr.State)

	channels := c.configManager.ChannelsForRepo(pr.Owner, pr.Repo)
	if len(channels) == 0 {
		slog.Debug("no channels configured for closed PR",
			"pr", prKey,
			"owner", pr.Owner,
			"repo", pr.Repo)
		return nil
	}

	updatedCount := 0
	for _, ch := range channels {
		id := c.slack.ResolveChannelID(ctx, ch)

		// Check if channel resolution failed (returns original name if not found)
		if id == ch || (ch != "" && ch[0] == '#' && id == ch[1:]) {
			slog.Warn("could not resolve channel for closed PR thread update",
				"workspace", c.workspaceName,
				"pr", prKey,
				"owner", pr.Owner,
				"repo", pr.Repo,
				"number", pr.Number,
				"channel", ch,
				"action_required", "verify channel exists and bot has access")
			continue
		}

		info, ok := c.stateStore.Thread(pr.Owner, pr.Repo, pr.Number, id)
		if !ok {
			// Thread not in persistent storage - search channel history as fallback
			// This handles cases where state was lost or thread created before persistence was added
			slog.Debug("thread not in state store, searching channel history",
				"pr", prKey,
				"channel", ch,
				"channel_id", id,
				"pr_state", pr.State)

			threadTS, messageText := c.searchForPRThread(ctx, id, pr.URL, pr.CreatedAt)
			if threadTS == "" {
				slog.Debug("no thread found in channel history for closed PR",
					"pr", prKey,
					"channel", ch,
					"channel_id", id,
					"pr_state", pr.State,
					"pr_created_at", pr.CreatedAt,
					"possible_reason", "PR closed before thread created or thread in different channel")
				continue
			}

			// Found via channel history - reconstruct ThreadInfo
			info = ThreadInfo{
				ThreadTS:    threadTS,
				ChannelID:   id,
				MessageText: messageText,
				UpdatedAt:   time.Now(),
			}

			// Persist for future use (avoid redundant searches)
			if err := c.stateStore.SaveThread(pr.Owner, pr.Repo, pr.Number, id, info); err != nil {
				slog.Warn("failed to persist recovered thread",
					"pr", prKey,
					"error", err)
			}

			slog.Info("found thread via channel history search",
				"pr", prKey,
				"channel", ch,
				"thread_ts", threadTS,
				"message_preview", messageText[:min(len(messageText), 100)])
		}

		if err := c.updateThreadForClosedPR(ctx, pr, id, info); err != nil {
			slog.Warn("failed to update thread for closed PR",
				"pr", prKey,
				"channel", ch,
				"error", err)
			continue
		}

		updatedCount++
		slog.Info("updated thread for closed/merged PR",
			"pr", prKey,
			"state", pr.State,
			"channel", ch,
			"thread_ts", info.ThreadTS)
	}

	if updatedCount == 0 {
		return errors.New("no threads found or updated for closed PR")
	}

	return nil
}

// updateThreadForClosedPR updates a single thread's message to reflect closed/merged state.
func (c *Coordinator) updateThreadForClosedPR(ctx context.Context, pr *github.PRSnapshot, channelID string, info ThreadInfo) error {
	var emoji string
	switch pr.State {
	case "MERGED":
		emoji = ":rocket:"
	case "CLOSED":
		emoji = ":x:"
	default:
		return fmt.Errorf("unexpected PR state: %s", pr.State)
	}

	// Replace emoji prefix in message (format: ":emoji: Title • repo#123 by @user")
	text := info.MessageText
	if i := strings.Index(text, " "); i == -1 {
		text = emoji + " " + text
	} else {
		text = emoji + text[i:]
	}

	if err := c.slack.UpdateMessage(ctx, channelID, info.ThreadTS, text); err != nil {
		return fmt.Errorf("failed to update message: %w", err)
	}

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
		lastNotified := c.stateStore.LastNotification(pr.URL)

		// Determine if we should notify
		var reason string
		switch {
		case lastNotified.IsZero():
			reason = "never_notified"
		case pr.UpdatedAt.After(lastNotified):
			reason = "updated_since_last_notification"
		default:
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
