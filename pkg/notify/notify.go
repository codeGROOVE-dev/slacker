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

// NotifyUser sends a notification to a user about a PR.
func (m *Manager) NotifyUser(ctx context.Context, workspaceID, userID string, pr *state.PRState) error {
	// Get user preferences.
	prefs := m.stateManager.UserPreferences(workspaceID, userID)

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

	// Send DM to user.
	if err := m.slack.SendDirectMessage(ctx, userID, message); err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}

	// Update last notified time.
	m.stateManager.UpdateLastNotified(workspaceID, userID)

	slog.Info("sent notification", "user", userID, "owner", pr.Owner, "repo", pr.Repo, "number", pr.Number)
	return nil
}
