// Package notify handles user notifications and reminders.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/slack"
)

// Manager handles user notifications across multiple workspaces.
type Manager struct {
	slackManager  *slack.Manager
	Tracker       *NotificationTracker
	configManager interface {
		DailyRemindersEnabled(org string) bool
	}
}

// New creates a new notification manager.
func New(slackManager *slack.Manager, configManager interface {
	DailyRemindersEnabled(org string) bool
},
) *Manager {
	return &Manager{
		slackManager: slackManager,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
		},
		configManager: configManager,
	}
}

// Run starts the notification scheduler.
func (*Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Check if any users need notifications.
			// This would iterate through workspaces and users.
			// For now, we'll implement a simplified version.
			slog.Debug("checking for pending notifications")
			// In production, this would:
			// 1. Iterate through all workspaces
			// 2. For each workspace, check all users with pending PRs
			// 3. Apply notification logic based on preferences
			// 4. Send notifications as needed
		}
	}
}

// PRInfo contains the minimal information needed to notify about a PR.
type PRInfo struct {
	Owner   string
	Repo    string
	Number  int
	Title   string
	Author  string
	State   string
	HTMLURL string
}

// NotifyUser sends a smart notification to a user about a PR using the configured logic.
func (m *Manager) NotifyUser(ctx context.Context, workspaceID, userID, channelID string, pr PRInfo) error {
	slog.Info("evaluating notification for user",
		"user", userID,
		"workspace", workspaceID,
		"owner", pr.Owner,
		"repo", pr.Repo,
		"pr_number", pr.Number,
		"pr_state", pr.State,
		"channel", channelID)

	// Get the Slack client for this workspace
	slackClient, err := m.slackManager.GetClient(ctx, workspaceID)
	if err != nil {
		slog.Error("failed to get Slack client for workspace",
			"workspace", workspaceID,
			"error", err)
		return fmt.Errorf("failed to get Slack client: %w", err)
	}

	lastDM := m.Tracker.LastDMNotification(workspaceID, userID)

	slog.Debug("notification state",
		"user", userID,
		"last_dm", lastDM)

	// Check if user is active on Slack.
	isActive := slackClient.IsUserActive(ctx, userID)
	slog.Debug("checking user activity status",
		"user", userID,
		"is_active", isActive)

	if !isActive {
		slog.Info("deferring notification - user not active on Slack",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"will_retry_when_active", true)
		return nil
	}

	// Avoid spamming - don't send DM if we recently sent one
	timeSinceLastDM := time.Since(lastDM)
	antiSpamDelay := 30 * time.Minute

	slog.Debug("checking anti-spam protection",
		"user", userID,
		"time_since_last_dm", timeSinceLastDM,
		"anti_spam_delay", antiSpamDelay,
		"will_block", timeSinceLastDM < antiSpamDelay)

	if timeSinceLastDM < antiSpamDelay {
		slog.Info("skipping DM - anti-spam protection active",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"time_since_last_dm", timeSinceLastDM,
			"anti_spam_delay", antiSpamDelay,
			"time_until_next_allowed", antiSpamDelay-timeSinceLastDM)
		return nil
	}

	// Format notification message
	var action string
	switch pr.State {
	case "broken_heart":
		action = "waiting for you to fix tests"
	case "hourglass":
		action = "waiting for your review"
	case "carpentry_saw":
		action = "waiting for you to address review feedback"
	case "check":
		action = "approved and ready to merge"
	default:
		action = "needs your attention"
	}

	message := fmt.Sprintf(
		":postal_horn: %s • %s/%s#%d by @%s - %s",
		pr.Title,
		pr.Owner,
		pr.Repo,
		pr.Number,
		pr.Author,
		action,
	)

	slog.Info("sending DM notification to user",
		"user", userID,
		"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
		"pr_state", pr.State,
		"action_required", action,
		"message", message)

	// Send DM to user.
	if err := slackClient.SendDirectMessage(ctx, userID, message); err != nil {
		slog.Error("failed to send DM notification",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"error", err,
			"will_retry", true)
		return fmt.Errorf("failed to send notification: %w", err)
	}

	// Update last DM notification time.
	m.Tracker.UpdateDMNotification(workspaceID, userID)

	slog.Info("successfully sent DM notification",
		"user", userID,
		"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
		"pr_author", pr.Author,
		"pr_state", pr.State,
		"action_required", action,
		"notification_updated", true)
	return nil
}
