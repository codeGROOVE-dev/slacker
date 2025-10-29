// Package notify handles user notifications and reminders.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// Constants for notification defaults.
const (
	defaultReminderDMDelayMinutes = 65 // Default delay in minutes before sending DM if user tagged in channel
)

// UserMapper interface for formatting user mentions in messages.
type UserMapper interface {
	FormatUserMentions(ctx context.Context, githubUsers []string, owner, domain string) string
}

// MessageParams contains all parameters needed to format a channel message.
//
//nolint:govet // fieldalignment optimization would reduce parameter clarity
type MessageParams struct {
	CheckResult *turn.CheckResponse
	Owner       string
	Repo        string
	PRNumber    int
	Title       string
	Author      string
	HTMLURL     string
	Domain      string
	UserMapper  UserMapper
}

// FormatChannelMessageBase formats the base Slack channel message for a PR (without next actions).
// This is the single source of truth for channel message formatting.
// It handles emoji determination and base message assembly.
// Use FormatNextActionsSuffix to add next actions when needed.
func FormatChannelMessageBase(ctx context.Context, params MessageParams) string {
	pr := params.CheckResult.PullRequest
	a := params.CheckResult.Analysis
	prID := fmt.Sprintf("%s/%s#%d", params.Owner, params.Repo, params.PRNumber)

	// Log all decision factors for debugging
	kinds := make([]string, 0, len(a.NextAction))
	for user, action := range a.NextAction {
		kinds = append(kinds, fmt.Sprintf("%s:%s", user, action.Kind))
	}

	slog.Info("formatting channel message",
		"pr", prID,
		"pr_state", pr.State,
		"merged", pr.Merged,
		"draft", pr.Draft,
		"workflow_state", a.WorkflowState,
		"next_action_count", len(a.NextAction),
		"next_actions", kinds,
		"checks_failing", a.Checks.Failing,
		"checks_pending", a.Checks.Pending,
		"checks_waiting", a.Checks.Waiting,
		"approved", a.Approved,
		"unresolved_comments", a.UnresolvedComments)

	// Determine emoji and state parameter
	var emoji, state string

	// Handle merged/closed states first (most definitive)
	//nolint:gocritic // if-else chain is clearer than switch for state-based logic
	if pr.Merged {
		emoji, state = ":rocket:", "?st=merged"
		slog.Info("using :rocket: emoji - PR is merged", "pr", prID, "merged_at", pr.MergedAt)
	} else if pr.State == "closed" {
		emoji, state = ":x:", "?st=closed"
		slog.Info("using :x: emoji - PR is closed but not merged", "pr", prID)
	} else if a.WorkflowState != "" {
		// Use WorkflowState as primary signal (most reliable source of truth)
		emoji, state = emojiFromWorkflowState(a.WorkflowState, a.NextAction)
		slog.Info("using emoji from workflow_state", "pr", prID, "workflow_state", a.WorkflowState, "emoji", emoji, "state_param", state)
	} else if len(a.NextAction) > 0 {
		// Fallback to NextAction if no WorkflowState (shouldn't normally happen)
		action := PrimaryAction(a.NextAction)
		emoji = PrefixForAction(action)
		state = stateParam(params.CheckResult)
		slog.Info("using emoji from primary next_action (no workflow_state)", "pr", prID, "primary_action", action, "emoji", emoji, "state_param", state)
	} else {
		// Final fallback based on PR properties
		emoji, state = fallbackEmoji(params.CheckResult)
		//nolint:revive // line length acceptable for structured logging
		slog.Info("using fallback emoji - no workflow_state or next_actions", "pr", prID, "emoji", emoji, "state_param", state, "fallback_reason", "empty_workflow_state_and_next_actions")
	}

	return fmt.Sprintf("%s %s <%s|%s#%d> · %s",
		emoji,
		params.Title,
		params.HTMLURL+state,
		params.Repo,
		params.PRNumber,
		params.Author)
}

// FormatNextActionsSuffix formats the next actions suffix for a channel message.
// Returns empty string if no next actions are present.
// Call this after FormatChannelMessageBase to build a complete message with user mentions.
func FormatNextActionsSuffix(ctx context.Context, params MessageParams) string {
	if params.CheckResult == nil || len(params.CheckResult.Analysis.NextAction) == 0 {
		return ""
	}

	actions := formatNextActionsInternal(ctx, params.CheckResult.Analysis.NextAction, params.Owner, params.Domain, params.UserMapper)
	if actions != "" {
		return fmt.Sprintf(" → %s", actions)
	}
	return ""
}

// emojiFromWorkflowState determines emoji based on WorkflowState as the primary signal.
// Uses NextAction for additional granularity in specific states (e.g., test failures).
func emojiFromWorkflowState(workflowState string, nextActions map[string]turn.Action) (emoji, state string) {
	switch workflowState {
	case string(turn.StateNewlyPublished):
		return ":new:", "?st=newly_published"

	case string(turn.StateInDraft):
		return ":construction:", "?st=draft"

	case string(turn.StatePublishedWaitingForTests):
		// Use NextAction to distinguish between broken tests vs pending tests
		if len(nextActions) > 0 {
			action := PrimaryAction(nextActions)
			if action == string(turn.ActionFixTests) {
				return ":cockroach:", "?st=tests_broken"
			}
			if action == string(turn.ActionTestsPending) {
				return ":test_tube:", "?st=tests_running"
			}
		}
		// Default to tests running
		return ":test_tube:", "?st=tests_running"

	case string(turn.StateTestedWaitingForAssignment):
		// Waiting for reviewers to be assigned
		return ":shrug:", "?st=awaiting_assignment"

	case string(turn.StateAssignedWaitingForReview):
		// Waiting for assigned reviewers to review
		return ":hourglass:", "?st=awaiting_review"

	case string(turn.StateReviewedNeedsRefinement):
		// Author needs to address feedback/comments
		return ":carpentry_saw:", "?st=changes_requested"

	case string(turn.StateRefinedWaitingForApproval):
		// Waiting for final approval after refinements
		return ":hourglass:", "?st=awaiting_approval"

	case string(turn.StateApprovedWaitingForMerge):
		// Approved and ready to merge
		return ":white_check_mark:", "?st=approved"

	default:
		// Unknown workflow state - fall back to NextAction
		slog.Warn("unknown workflow state, falling back to NextAction",
			"workflow_state", workflowState)
		if len(nextActions) > 0 {
			action := PrimaryAction(nextActions)
			return PrefixForAction(action), "?st=unknown"
		}
		return ":postal_horn:", "?st=unknown"
	}
}

// stateParam returns the URL state parameter based on PR analysis.
func stateParam(r *turn.CheckResponse) string {
	pr := r.PullRequest
	a := r.Analysis

	if pr.Draft {
		return "?st=tests_running"
	}
	if a.Checks.Failing > 0 {
		return "?st=tests_broken"
	}
	if a.Checks.Pending > 0 || a.Checks.Waiting > 0 {
		return "?st=tests_running"
	}
	if a.Approved {
		if a.UnresolvedComments > 0 {
			return "?st=changes_requested"
		}
		return "?st=approved"
	}
	return "?st=awaiting_review"
}

// fallbackEmoji determines emoji when no workflow_state or next_actions are available.
func fallbackEmoji(r *turn.CheckResponse) (emoji, state string) {
	pr := r.PullRequest
	a := r.Analysis

	if pr.Draft {
		return ":test_tube:", "?st=tests_running"
	}
	if a.Checks.Failing > 0 {
		return ":cockroach:", "?st=tests_broken"
	}
	if a.Checks.Pending > 0 || a.Checks.Waiting > 0 {
		return ":test_tube:", "?st=tests_running"
	}
	if a.Approved {
		if a.UnresolvedComments > 0 {
			return ":carpentry_saw:", "?st=changes_requested"
		}
		return ":white_check_mark:", "?st=approved"
	}
	return ":hourglass:", "?st=awaiting_review"
}

// formatNextActionsInternal formats next actions with user mentions (internal helper).
func formatNextActionsInternal(ctx context.Context, nextActions map[string]turn.Action, owner, domain string, userMapper UserMapper) string {
	if len(nextActions) == 0 {
		return ""
	}

	// Group users by action kind, filtering out _system users
	actionGroups := make(map[string][]string)
	for user, action := range nextActions {
		actionKind := string(action.Kind)
		// Skip _system users but still track the action exists
		if user != "_system" {
			actionGroups[actionKind] = append(actionGroups[actionKind], user)
		} else if _, exists := actionGroups[actionKind]; !exists {
			// Action only has _system - create empty slice to track it exists
			actionGroups[actionKind] = []string{}
		}
	}

	// Format each action group
	var parts []string
	for actionKind, users := range actionGroups {
		// Convert snake_case to space-separated words
		actionName := strings.ReplaceAll(actionKind, "_", " ")

		// Format user mentions (will be empty if only _system was assigned)
		userMentions := userMapper.FormatUserMentions(ctx, users, owner, domain)

		// If action has users, format as "action: users"
		// If no users (was only _system), just show the action
		if userMentions != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", actionName, userMentions))
		} else {
			parts = append(parts, actionName)
		}
	}

	// Use semicolons to separate different actions (commas are used between users)
	return strings.Join(parts, "; ")
}

// Manager handles user notifications across multiple workspaces.
type Manager struct {
	slackManager  SlackManager
	Tracker       *NotificationTracker
	configManager ConfigManager
}

// New creates a new notification manager.
func New(slackManager SlackManager, configManager ConfigManager) *Manager {
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
//
//nolint:govet // fieldalignment optimization would reduce clarity
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
// 1. Use WorkflowState as primary signal (most reliable)
// 2. Fall back to NextAction if no WorkflowState
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

	// Use WorkflowState as primary signal
	if workflowState != "" {
		emoji, _ := emojiFromWorkflowState(workflowState, nextActions)
		slog.Debug("determined emoji from workflow_state",
			"workflow_state", workflowState,
			"emoji", emoji)
		return emoji
	}

	// Fallback to NextAction if no WorkflowState
	primaryAction := PrimaryAction(nextActions)
	if primaryAction != "" {
		emoji := PrefixForAction(primaryAction)
		slog.Debug("determined emoji from primary action (no workflow_state)",
			"primary_action", primaryAction,
			"emoji", emoji)
		return emoji
	}

	// Final fallback if no workflow state or actions
	slog.Info("no workflow_state or primary action - using fallback emoji",
		"next_actions_count", len(nextActions),
		"fallback_emoji", ":postal_horn:")
	return ":postal_horn:"
}

// NotifyUser sends a smart notification to a user about a PR using the configured logic.
// Implements delayed DM logic: if user was tagged in channel, delay by configured time.
// If user is not in channel where tagged, send DM immediately.
//
//nolint:revive,maintidx // function length acceptable for complex notification logic
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
