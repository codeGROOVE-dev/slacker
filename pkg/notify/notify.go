// Package notify handles user notifications and reminders.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
)

// Manager handles user notifications.
type Manager struct {
	slack         *slack.Client
	stateManager  *state.Manager
	configManager interface {
		ChannelNotifyDelayMins(org string) int
		DailyRemindersEnabled(org string) bool
	}
}

// New creates a new notification manager.
func New(slackClient *slack.Client, stateManager *state.Manager, configManager interface {
	ChannelNotifyDelayMins(org string) int
	DailyRemindersEnabled(org string) bool
},
) *Manager {
	return &Manager{
		slack:         slackClient,
		stateManager:  stateManager,
		configManager: configManager,
	}
}

// Run starts the notification scheduler.
func (m *Manager) Run(ctx context.Context) error {
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

// NotifyUser sends a smart notification to a user about a PR using the configured logic.
func (m *Manager) NotifyUser(ctx context.Context, workspaceID, userID, channelID string, pr *state.PRState) error {
	slog.Info("evaluating notification for user",
		"user", userID,
		"workspace", workspaceID,
		"owner", pr.Owner,
		"repo", pr.Repo,
		"pr_number", pr.Number,
		"pr_state", pr.State,
		"channel", channelID,
		"blocked_on", pr.BlockedOn)

	notificationState := m.stateManager.GetNotificationState(workspaceID, userID)
	delayMins := m.configManager.ChannelNotifyDelayMins(pr.Owner)

	slog.Debug("notification state and config",
		"user", userID,
		"last_dm", notificationState.LastDMNotification,
		"last_daily", notificationState.LastDailyReminder,
		"delay_config_mins", delayMins,
		"pr_last_channel_notification", pr.LastChannelNotification)

	// Smart logic: If we've already tagged user in a public channel they are in,
	// don't send DM until it's been an hour since channel notification and
	// they haven't reacted or participated in the thread or updated the PR.
	if !pr.LastChannelNotification.IsZero() && channelID != "" {
		// User was notified in a channel - apply delay logic
		timeSinceChannelNotification := time.Since(pr.LastChannelNotification)
		delayDuration := time.Duration(delayMins) * time.Minute

		slog.Info("applying channel notification delay logic",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"time_since_channel_notification", timeSinceChannelNotification,
			"configured_delay", delayDuration,
			"will_delay", timeSinceChannelNotification < delayDuration)

		if timeSinceChannelNotification < delayDuration {
			slog.Info("skipping DM - user recently notified in channel, applying delay",
				"user", userID,
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"channel", channelID,
				"time_since_channel", timeSinceChannelNotification,
				"delay_remaining", delayDuration-timeSinceChannelNotification)
			return nil
		}

		slog.Info("channel notification delay period expired, proceeding with DM",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"time_since_channel", timeSinceChannelNotification,
			"delay_duration", delayDuration)

		// TODO: Check if user has reacted or participated in thread
		// TODO: Check if PR has been updated
		// For now, proceed with notification after delay
	} else {
		if pr.LastChannelNotification.IsZero() {
			slog.Info("no prior channel notification found, sending immediate DM",
				"user", userID,
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"reason", "user_not_notified_in_channel")
		} else {
			slog.Info("user not in notification channel, sending immediate DM",
				"user", userID,
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"channel", channelID,
				"reason", "user_not_in_channel")
		}
	}

	// Check if user is active on Slack.
	isActive := m.slack.IsUserActive(ctx, userID)
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
	timeSinceLastDM := time.Since(notificationState.LastDMNotification)
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
	if err := m.slack.SendDirectMessage(ctx, userID, message); err != nil {
		slog.Error("failed to send DM notification",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"error", err,
			"will_retry", true)
		return fmt.Errorf("failed to send notification: %w", err)
	}

	// Update last DM notification time.
	m.stateManager.UpdateDMNotification(workspaceID, userID)

	slog.Info("successfully sent DM notification",
		"user", userID,
		"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
		"pr_author", pr.Author,
		"pr_state", pr.State,
		"action_required", action,
		"notification_updated", true)
	return nil
}
