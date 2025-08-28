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
	slack        *slack.Client
	stateManager *state.Manager
}

// New creates a new notification manager.
func New(slackClient *slack.Client, stateManager *state.Manager) *Manager {
	return &Manager{
		slack:        slackClient,
		stateManager: stateManager,
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
			m.checkNotifications(ctx)
		}
	}
}

// checkNotifications checks if any users need notifications.
func (m *Manager) checkNotifications(ctx context.Context) {
	// This would iterate through workspaces and users.
	// For now, we'll implement a simplified version.
	slog.Debug("checking for pending notifications")

	// In production, this would:
	// 1. Iterate through all workspaces
	// 2. For each workspace, check all users with pending PRs
	// 3. Apply notification logic based on preferences
	// 4. Send notifications as needed
}

// NotifyUser sends a notification to a user about a PR.
func (m *Manager) NotifyUser(ctx context.Context, workspaceID, userID string, pr *state.PRState) error {
	// Get user preferences.
	prefs := m.stateManager.GetUserPreferences(workspaceID, userID)

	// Check if real-time notifications are enabled.
	if !prefs.RealTimeNotifications {
		return nil
	}

	// Check if enough time has passed since last notification.
	if time.Since(prefs.LastNotified) < prefs.ChannelNotifyDelay {
		slog.Debug("skipping notification - too soon", "user", userID)
		return nil
	}

	// Check if user is active.
	if !m.slack.IsUserActive(ctx, userID) {
		slog.Debug("user not active, deferring notification", "user", userID)
		return nil
	}

	// Format notification message.
	message := m.formatNotificationMessage(pr)

	// Send DM to user.
	if err := m.slack.SendDirectMessage(ctx, userID, message); err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}

	// Update last notified time.
	m.stateManager.UpdateLastNotified(workspaceID, userID)

	slog.Info("sent notification", "user", userID, "owner", pr.Owner, "repo", pr.Repo, "number", pr.Number)
	return nil
}

// formatNotificationMessage formats a notification message for a PR.
func (m *Manager) formatNotificationMessage(pr *state.PRState) string {
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

	return fmt.Sprintf(
		":postal_horn: %s • %s/%s#%d by @%s - %s",
		pr.Title,
		pr.Owner,
		pr.Repo,
		pr.Number,
		pr.Author,
		action,
	)
}

// CheckDailyReminders checks and sends daily reminders.
func (m *Manager) CheckDailyReminders(ctx context.Context, workspaceID string) error {
	// This would be called periodically to send daily reminders.
	// It would check each user's timezone and preferences.
	slog.Debug("checking daily reminders", "workspace", workspaceID)

	// In production:
	// 1. Get all users in workspace
	// 2. For each user, check if it's 8-9am in their timezone
	// 3. Check if daily reminders are enabled
	// 4. Check if >8 hours since last notification
	// 5. Send summary of blocked PRs

	return nil
}

// SendThreadUpdate sends an update to a PR thread.
func (m *Manager) SendThreadUpdate(ctx context.Context, channelID, threadTS, message string) error {
	return m.slack.PostThreadReply(ctx, channelID, threadTS, message)
}

// UpdateThreadReaction updates the reaction on a thread based on PR state.
func (m *Manager) UpdateThreadReaction(ctx context.Context, channelID, timestamp, newState string) error {
	return m.slack.UpdateReactions(ctx, channelID, timestamp, newState)
}
