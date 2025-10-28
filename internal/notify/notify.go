// Package notify handles user notifications and reminders.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/slack"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// Constants for notification defaults.
const (
	defaultReminderDMDelayMinutes = 65 // Default delay in minutes before sending DM if user tagged in channel
)

// Manager handles user notifications across multiple workspaces.
type Manager struct {
	slackManager  *slack.Manager
	Tracker       *NotificationTracker
	configManager interface {
		DailyRemindersEnabled(org string) bool
		ReminderDMDelay(org, channel string) int
	}
}

// New creates a new notification manager.
func New(slackManager *slack.Manager, configManager interface {
	DailyRemindersEnabled(org string) bool
	ReminderDMDelay(org, channel string) int
},
) *Manager {
	return &Manager{
		slackManager: slackManager,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: configManager,
	}
}

// Run starts the notification scheduler.
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Cleanup old entries every hour to prevent unbounded memory growth
	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

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
		case <-cleanupTicker.C:
			// Clean up entries older than 7 days
			// This keeps recent data for rate limiting while preventing unbounded growth
			m.Tracker.Cleanup(7 * 24 * time.Hour)
			slog.Debug("cleaned up old notification tracker entries")
		}
	}
}

// PRInfo contains the minimal information needed to notify about a PR.
type PRInfo struct {
	Owner         string
	Repo          string
	Title         string
	Author        string
	State         string // Deprecated: use WorkflowState and NextAction for emoji determination
	HTMLURL       string
	Number        int
	WorkflowState string                 // Workflow state from turnclient Analysis
	NextAction    map[string]turn.Action // Next actions from turnclient Analysis
}

// PrefixForState returns the emoji prefix for a given PR state.
// Exported for use by bot package to ensure consistent PR state display.
//
// Deprecated: Use PrefixForAnalysis instead to derive emoji from NextAction.
func PrefixForState(prState string) string {
	switch prState {
	case "newly_published":
		return ":new:"
	case "tests_running":
		return ":test_tube:"
	case "tests_broken":
		return ":cockroach:"
	case "awaiting_review":
		return ":hourglass:"
	case "changes_requested":
		return ":carpentry_saw:"
	case "approved":
		return ":white_check_mark:"
	case "merged":
		return ":rocket:"
	case "closed":
		return ":x:"
	default:
		return ":postal_horn:"
	}
}

// PrefixForAction returns the emoji prefix for a given action kind.
func PrefixForAction(action string) string {
	switch action {
	case "publish_draft":
		return ":construction:"
	case "fix_tests":
		return ":cockroach:"
	case "tests_pending":
		return ":test_tube:"
	case "review", "re_review", "request_reviewers":
		return ":hourglass:"
	case "resolve_comments", "respond", "review_discussion":
		return ":carpentry_saw:"
	case "approve":
		return ":white_check_mark:"
	case "merge":
		return ":rocket:"
	default:
		return ":postal_horn:"
	}
}

// actionPriority returns a priority score for action kinds (lower = higher priority).
// Used to determine which emoji to show when multiple actions are pending.
func actionPriority(action string) int {
	switch action {
	case "publish_draft":
		return 1
	case "fix_tests":
		return 2
	case "tests_pending":
		return 3
	case "fix_conflict":
		return 4
	case "resolve_comments", "respond", "review_discussion":
		return 5
	case "review", "re_review", "request_reviewers":
		return 6
	case "approve":
		return 7
	case "merge":
		return 8
	default:
		return 99
	}
}

// PrimaryAction determines the primary action kind from a NextAction map.
// Returns the highest-priority action (lowest priority score).
func PrimaryAction(nextActions map[string]turn.Action) string {
	if len(nextActions) == 0 {
		return ""
	}

	var primaryAction string
	minPriority := 999

	// Track all actions considered for debugging
	actionPriorities := make(map[string]int)

	for _, action := range nextActions {
		kind := string(action.Kind)
		priority := actionPriority(kind)
		actionPriorities[kind] = priority
		if priority < minPriority {
			minPriority = priority
			primaryAction = kind
		}
	}

	slog.Debug("determined primary action from priorities",
		"primary_action", primaryAction,
		"primary_priority", minPriority,
		"all_action_priorities", actionPriorities)

	return primaryAction
}

// PrefixForAnalysis returns the emoji prefix based on workflow state and next actions.
// This is the primary function for determining PR emoji - it handles the logic:
// 1. If workflow_state == "newly_published" → ":new:"
// 2. Otherwise → emoji based on primary next_action
func PrefixForAnalysis(workflowState string, nextActions map[string]turn.Action) string {
	// Log input for debugging emoji selection
	actionKinds := make([]string, 0, len(nextActions))
	for user, action := range nextActions {
		actionKinds = append(actionKinds, fmt.Sprintf("%s:%s", user, action.Kind))
	}
	slog.Debug("determining emoji prefix",
		"workflow_state", workflowState,
		"next_actions_count", len(nextActions),
		"next_actions", actionKinds)

	// Special case: newly published PRs always show :new:
	if workflowState == "newly_published" {
		slog.Debug("using :new: emoji for newly published PR")
		return ":new:"
	}

	// Determine primary action and return corresponding emoji
	primaryAction := PrimaryAction(nextActions)
	if primaryAction != "" {
		emoji := PrefixForAction(primaryAction)
		slog.Debug("determined emoji from primary action",
			"primary_action", primaryAction,
			"emoji", emoji)
		return emoji
	}

	// Fallback if no actions
	slog.Info("no primary action found - using fallback emoji",
		"workflow_state", workflowState,
		"next_actions_count", len(nextActions),
		"fallback_emoji", ":postal_horn:")
	return ":postal_horn:"
}

// NotifyUser sends a smart notification to a user about a PR using the configured logic.
// Implements delayed DM logic: if user was tagged in channel, delay by configured time.
// If user is not in channel where tagged, send DM immediately.
func (m *Manager) NotifyUser(ctx context.Context, workspaceID, userID, channelID, channelName string, pr PRInfo) error {
	slog.Info("evaluating notification for user",
		"user", userID,
		"workspace", workspaceID,
		"owner", pr.Owner,
		"repo", pr.Repo,
		"pr_number", pr.Number,
		"pr_state", pr.State,
		"channel", channelID,
		"channel_name", channelName)

	// Get the Slack client for this workspace
	slackClient, err := m.slackManager.Client(ctx, workspaceID)
	if err != nil {
		slog.Error("failed to get Slack client for workspace",
			"workspace", workspaceID,
			"error", err)
		return fmt.Errorf("failed to get Slack client: %w", err)
	}

	lastDM := m.Tracker.LastDMNotification(workspaceID, userID)
	tagInfo := m.Tracker.LastUserPRChannelTag(workspaceID, userID, pr.Owner, pr.Repo, pr.Number)

	slog.Debug("notification state",
		"user", userID,
		"last_dm", lastDM,
		"last_channel_tag", tagInfo.Timestamp,
		"tag_channel_id", tagInfo.ChannelID)

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
	antiSpamDelay := 1 * time.Minute

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

	// Check if we should delay this DM based on channel tag timing
	if !tagInfo.Timestamp.IsZero() {
		// User was tagged in a channel - use the ACTUAL channel they were tagged in
		taggedChannelID := tagInfo.ChannelID

		// Check if they're in that specific channel
		userInChannel := slackClient.IsUserInChannel(ctx, taggedChannelID, userID)

		// Get configured delay for this channel/org (we need channel name for config lookup)
		// If channelName wasn't provided, we can't look up config - use default
		delayMins := defaultReminderDMDelayMinutes
		if channelName != "" {
			delayMins = m.configManager.ReminderDMDelay(pr.Owner, channelName)
		}

		slog.Debug("evaluating follow-up reminder delay",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"user_in_channel", userInChannel,
			"channel_tag_time", tagInfo.Timestamp,
			"tagged_channel_id", taggedChannelID,
			"configured_delay_mins", delayMins)

		if delayMins == 0 {
			slog.Info("follow-up reminders disabled for this channel - skipping DM",
				"user", userID,
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"channel", channelName,
				"channel_id", taggedChannelID)
			return nil
		}

		if userInChannel {
			// User is in the channel - apply delay
			timeSinceTag := time.Since(tagInfo.Timestamp)
			delayDuration := time.Duration(delayMins) * time.Minute

			if timeSinceTag < delayDuration {
				slog.Info("deferring DM - user was tagged in channel recently",
					"user", userID,
					"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
					"channel_id", taggedChannelID,
					"time_since_tag", timeSinceTag,
					"configured_delay", delayDuration,
					"time_until_dm", delayDuration-timeSinceTag)
				return nil
			}

			slog.Info("sending delayed follow-up DM - user was tagged but delay elapsed",
				"user", userID,
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"channel_id", taggedChannelID,
				"time_since_tag", timeSinceTag,
				"configured_delay", delayDuration)
		} else {
			// User is NOT in the channel - send DM immediately
			slog.Info("sending immediate DM - user not in channel where tagged",
				"user", userID,
				"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
				"channel_id", taggedChannelID)
		}
	} else {
		slog.Debug("no channel tag found - sending DM without delay",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number))
	}

	// Format notification message using same style as channel messages
	// Determine emoji prefix based on workflow state and next actions
	var prefix string
	if pr.WorkflowState != "" {
		prefix = PrefixForAnalysis(pr.WorkflowState, pr.NextAction)
	} else {
		// Fallback to state if workflow state not available
		prefix = PrefixForState(pr.State)
	}

	// Format: :emoji: Title <url|repo#123> · author → action
	var action string
	switch pr.State {
	case "newly_published":
		action = "newly published"
	case "tests_broken":
		action = "fix tests"
	case "awaiting_review":
		action = "review"
	case "changes_requested":
		action = "address feedback"
	case "approved":
		action = "merge"
	default:
		action = "attention needed"
	}

	// Use same compact format as channel messages
	message := fmt.Sprintf(
		"%s %s <%s|%s#%d> · %s → %s",
		prefix,
		pr.Title,
		pr.HTMLURL,
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

	// Check if we recently sent a DM about this PR (prevents duplicates during rolling deployments)
	hasRecent, err := slackClient.HasRecentDMAboutPR(ctx, userID, pr.HTMLURL)
	if err != nil {
		slog.Warn("failed to check for recent DM, will send anyway to avoid false negative",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"error", err,
			"check_window", "1h",
			"impact", "possible_duplicate_dm")
	} else if hasRecent {
		slog.Info("skipping DM - already sent notification about this PR recently",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"check_window", "1h",
			"reason", "duplicate prevention during rolling deployment",
			"will_retry_later", false)
		return nil
	}

	// Send DM to user.
	dmChannelID, messageTS, err := slackClient.SendDirectMessage(ctx, userID, message)
	if err != nil {
		slog.Error("failed to send DM notification",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"error", err,
			"will_retry", true)
		return fmt.Errorf("failed to send notification: %w", err)
	}

	// Update last DM notification time.
	m.Tracker.UpdateDMNotification(workspaceID, userID)

	// Save DM message info for future updates
	if err := slackClient.SaveDMMessageInfo(ctx, userID, pr.HTMLURL, dmChannelID, messageTS, message); err != nil {
		slog.Warn("failed to save DM message info",
			"user", userID,
			"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
			"error", err,
			"impact", "DM won't be updated on state changes")
	}

	slog.Info("successfully sent DM notification",
		"user", userID,
		"pr", fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number),
		"pr_author", pr.Author,
		"pr_state", pr.State,
		"action_required", action,
		"notification_updated", true,
		"dm_channel_id", dmChannelID,
		"message_ts", messageTS)
	return nil
}
