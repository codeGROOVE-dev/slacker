package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	slackapi "github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// dmNotificationRequest contains all data needed to send a DM notification.
type dmNotificationRequest struct {
	CheckResult *turn.CheckResponse
	UserID      string // Slack user ID
	ChannelID   string // Channel where user was tagged (empty if none)
	ChannelName string // Channel name for config lookup
	Owner       string
	Repo        string
	PRTitle     string
	PRAuthor    string
	PRURL       string
	PRNumber    int
}

// sendPRNotification is the single function that handles all DM operations.
// It's idempotent - only sends/updates if state changed for this user.
// Updates to existing DMs happen immediately (no delay).
// New DMs respect reminder_dm_delay (queue for later if user in channel).
func (c *Coordinator) sendPRNotification(ctx context.Context, req dmNotificationRequest) error {
	// Lock per user+PR to prevent concurrent goroutines from sending duplicate DMs
	lockKey := req.UserID + ":" + req.PRURL
	lockValue, _ := c.dmLocks.LoadOrStore(lockKey, &sync.Mutex{})
	mu := lockValue.(*sync.Mutex) //nolint:errcheck,revive // Type assertion always succeeds - we control what's stored
	mu.Lock()
	defer mu.Unlock()

	prState := derivePRState(req.CheckResult)

	// Get last notification from datastore
	lastNotif, exists := c.stateStore.DMMessage(ctx, req.UserID, req.PRURL)

	// Idempotency: skip if state unchanged
	// The per-user-PR lock above ensures no race conditions from concurrent calls
	// This check ensures we only send/update when the PR state actually changes
	if exists && lastNotif.LastState == prState {
		slog.Debug("DM skipped - state unchanged",
			"user", req.UserID,
			"pr", req.PRURL,
			"state", prState)
		return nil
	}

	// Format message (same as channel messages for consistency)
	msgParams := notify.MessageParams{
		CheckResult: req.CheckResult,
		Owner:       req.Owner,
		Repo:        req.Repo,
		PRNumber:    req.PRNumber,
		Title:       req.PRTitle,
		Author:      req.PRAuthor,
		HTMLURL:     req.PRURL,
		Domain:      c.configManager.Domain(req.Owner),
		ChannelName: "", // Not used for DMs
		UserMapper:  c.userMapper,
	}
	message := notify.FormatChannelMessageBase(ctx, msgParams) + notify.FormatNextActionsSuffix(ctx, msgParams)

	var dmLocations []slackapi.DMLocation

	// Try to find existing DM location
	if exists && lastNotif.ChannelID != "" && lastNotif.MessageTS != "" {
		// We know where the DM is from cache/datastore
		dmLocations = []slackapi.DMLocation{{
			ChannelID: lastNotif.ChannelID,
			MessageTS: lastNotif.MessageTS,
		}}
	} else {
		// Don't know where DM is - search history
		locations, err := c.findDMInHistory(ctx, req.UserID, req.PRURL)
		if err != nil {
			slog.Warn("DM history search failed",
				"user", req.UserID,
				"pr", req.PRURL,
				"error", err)
		} else if len(locations) > 0 {
			dmLocations = locations
		}
	}

	// Path 1: Update existing DMs immediately (no delay for updates)
	if len(dmLocations) > 0 {
		updated := false
		var finalChannelID, finalMessageTS string
		for _, loc := range dmLocations {
			if err := c.slack.UpdateMessage(ctx, loc.ChannelID, loc.MessageTS, message); err != nil {
				slog.Warn("failed to update DM",
					"user", req.UserID,
					"pr", req.PRURL,
					"channel_id", loc.ChannelID,
					"message_ts", loc.MessageTS,
					"error", err)
			} else {
				slog.Info("updated existing DM",
					"user", req.UserID,
					"pr", req.PRURL,
					"channel_id", loc.ChannelID,
					"message_ts", loc.MessageTS,
					"old_state", getLastState(lastNotif, exists),
					"new_state", prState)
				updated = true
				// Remember first successful update for cache
				if finalChannelID == "" {
					finalChannelID = loc.ChannelID
					finalMessageTS = loc.MessageTS
				}
			}
		}

		if updated {
			// Save notification state (memory + datastore)
			if err := c.stateStore.SaveDMMessage(ctx, req.UserID, req.PRURL, state.DMInfo{
				SentAt:      getSentAt(lastNotif, exists),
				UpdatedAt:   time.Now(),
				ChannelID:   finalChannelID,
				MessageTS:   finalMessageTS,
				MessageText: message,
				LastState:   prState,
			}); err != nil {
				slog.Warn("failed to save DM state after update",
					"user", req.UserID,
					"pr", req.PRURL,
					"error", err)
			}
			return nil
		}
		// All updates failed - fall through to send new DM
	}

	// Path 2: Send new DM (check delay logic)
	shouldQueue, sendAfter := c.shouldDelayNewDM(ctx, req.UserID, req.ChannelID, req.ChannelName, req.Owner, req.Repo)

	if shouldQueue {
		// Queue for later delivery
		slog.Info("queueing DM for delayed delivery",
			"user", req.UserID,
			"pr", req.PRURL,
			"send_after", sendAfter)
		return c.queueDMForUser(ctx, req, prState, sendAfter)
	}

	// Send immediately
	dmChannelID, messageTS, err := c.slack.SendDirectMessage(ctx, req.UserID, message)
	if err != nil {
		return fmt.Errorf("send DM: %w", err)
	}

	slog.Info("sent new DM",
		"user", req.UserID,
		"pr", req.PRURL,
		"channel_id", dmChannelID,
		"message_ts", messageTS,
		"state", prState)

	// Save notification state (memory + datastore)
	now := time.Now()
	if err := c.stateStore.SaveDMMessage(ctx, req.UserID, req.PRURL, state.DMInfo{
		SentAt:      now,
		UpdatedAt:   now,
		ChannelID:   dmChannelID,
		MessageTS:   messageTS,
		MessageText: message,
		LastState:   prState,
	}); err != nil {
		slog.Warn("failed to save DM state after send",
			"user", req.UserID,
			"pr", req.PRURL,
			"error", err)
	}

	return nil
}

// findDMInHistory searches Slack DM history to find existing messages about a PR.
// This is the fallback when cache/datastore don't have the DM location.
// Searches last 7 days of DM history using the Slack API directly.
func (c *Coordinator) findDMInHistory(ctx context.Context, userID, prURL string) ([]slackapi.DMLocation, error) {
	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour)
	locations, err := c.slack.FindDMMessagesInHistory(ctx, userID, prURL, sevenDaysAgo)
	if err != nil {
		return nil, err
	}

	if len(locations) == 0 {
		slog.Debug("no existing DM found in history",
			"user", userID,
			"pr", prURL,
			"searched_days", 7)
		return nil, nil
	}

	if len(locations) > 1 {
		slog.Warn("found multiple DMs for same PR - will update all",
			"user", userID,
			"pr", prURL,
			"count", len(locations))
	}

	slog.Info("found existing DM(s) in history",
		"user", userID,
		"pr", prURL,
		"count", len(locations))

	return locations, nil
}

// shouldDelayNewDM determines if a new DM should be queued for later.
// Returns (shouldQueue bool, sendAfter time.Time).
// Simplified version of evaluateDMDelay - removes user presence checking and anti-spam.
func (c *Coordinator) shouldDelayNewDM(
	ctx context.Context,
	userID, channelID, channelName string,
	owner, _ string,
) (bool, time.Time) {
	// Get configured delay for this channel (in minutes)
	delayMinutes := c.configManager.ReminderDMDelay(owner, channelName)
	delay := time.Duration(delayMinutes) * time.Minute

	// If delay is 0, feature is disabled - send immediately
	if delay == 0 {
		return false, time.Time{}
	}

	// If user wasn't tagged in a channel, send immediately
	if channelID == "" {
		return false, time.Time{}
	}

	// Check if user is in the channel where they were tagged
	isInChannel := c.slack.IsUserInChannel(ctx, channelID, userID)

	// If user is NOT in channel, they can't see the tag - send immediately
	if !isInChannel {
		slog.Debug("user not in channel, sending DM immediately",
			"user", userID,
			"channel", channelID)
		return false, time.Time{}
	}

	// User is in channel - queue for delayed delivery
	sendAfter := time.Now().Add(delay)
	return true, sendAfter
}

// queueDMForUser queues a DM to be sent later by the notify scheduler.
// Queues directly to state store - the notify.Manager scheduler will process it.
func (c *Coordinator) queueDMForUser(ctx context.Context, req dmNotificationRequest, prState string, sendAfter time.Time) error {
	checkResult := req.CheckResult
	// Serialize NextAction map to JSON
	actionsJSON, err := json.Marshal(checkResult.Analysis.NextAction)
	if err != nil {
		slog.Error("failed to serialize next actions",
			"user", req.UserID,
			"pr", fmt.Sprintf("%s/%s#%d", req.Owner, req.Repo, req.PRNumber),
			"error", err)
		actionsJSON = []byte("{}")
	}

	// Create pending DM record
	dm := &state.PendingDM{
		ID:            generateUUID(),
		WorkspaceID:   c.configManager.WorkspaceName(req.Owner),
		UserID:        req.UserID,
		PROwner:       req.Owner,
		PRRepo:        req.Repo,
		PRNumber:      req.PRNumber,
		PRURL:         req.PRURL,
		PRTitle:       req.PRTitle,
		PRAuthor:      req.PRAuthor,
		PRState:       prState,
		WorkflowState: checkResult.Analysis.WorkflowState,
		NextActions:   string(actionsJSON),
		ChannelID:     req.ChannelID,
		ChannelName:   req.ChannelName,
		QueuedAt:      time.Now(),
		SendAfter:     sendAfter,
	}

	// Queue to state store - the notify scheduler will process it
	if err := c.stateStore.QueuePendingDM(ctx, dm); err != nil {
		return err
	}

	// Save DM state immediately (with placeholder) so subsequent updates know about it
	// This prevents duplicate DMs when multiple webhook events arrive concurrently
	now := time.Now()
	if err := c.stateStore.SaveDMMessage(ctx, req.UserID, req.PRURL, state.DMInfo{
		SentAt:      now,
		UpdatedAt:   now,
		ChannelID:   "", // Will be filled in when actually sent
		MessageTS:   "", // Will be filled in when actually sent
		MessageText: "",
		LastState:   prState,
	}); err != nil {
		slog.Warn("failed to save DM state after queueing",
			"user", req.UserID,
			"pr", req.PRURL,
			"error", err)
	}

	return nil
}

// generateUUID creates a simple UUID for pending DM tracking.
func generateUUID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix())
}

// derivePRState extracts a simple state string from turnclient analysis.
func derivePRState(checkResult *turn.CheckResponse) string {
	if checkResult == nil {
		return "unknown"
	}
	return checkResult.Analysis.WorkflowState
}

// getLastState returns the last state from state.DMInfo if it exists, otherwise "none".
func getLastState(info state.DMInfo, exists bool) string {
	if !exists || info.LastState == "" {
		return "none"
	}
	return info.LastState
}

// getSentAt returns the SentAt time from state.DMInfo if it exists, otherwise now.
func getSentAt(info state.DMInfo, exists bool) time.Time {
	if !exists || info.SentAt.IsZero() {
		return time.Now()
	}
	return info.SentAt
}

// sendDMNotificationsToTaggedUsers sends DM notifications to Slack users who were tagged in channels.
// This runs in a separate goroutine to avoid blocking event processing.
// Uses the simplified sendPRNotification() for all DM operations.
func (c *Coordinator) sendDMNotificationsToTaggedUsers(
	ctx context.Context, workspaceID, owner, repo string,
	prNumber int, slackUsers map[string]bool,
	event struct {
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
	},
	checkResult *turn.CheckResponse,
) {
	slog.Info("starting DM notification batch for tagged Slack users",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"workspace", workspaceID,
		"user_count", len(slackUsers))

	sentCount := 0
	failedCount := 0

	for slackUserID := range slackUsers {
		// Get tag info to determine which channel the user was tagged in
		var channelID string
		if c.notifier != nil && c.notifier.Tracker != nil {
			tagInfo := c.notifier.Tracker.LastUserPRChannelTag(workspaceID, slackUserID, owner, repo, prNumber)
			channelID = tagInfo.ChannelID
		}

		// ChannelName is not available (no reverse lookup), so pass empty string
		// The delay logic will use the default config for the org
		err := c.sendPRNotification(ctx, dmNotificationRequest{
			UserID:      slackUserID,
			ChannelID:   channelID,
			ChannelName: "", // not available
			Owner:       owner,
			Repo:        repo,
			PRNumber:    prNumber,
			PRTitle:     event.PullRequest.Title,
			PRAuthor:    event.PullRequest.User.Login,
			PRURL:       event.PullRequest.HTMLURL,
			CheckResult: checkResult,
		})
		if err != nil {
			slog.Warn("failed to notify user",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
				"slack_user", slackUserID,
				"error", err)
			failedCount++
		} else {
			sentCount++
		}
	}

	slog.Info("completed DM notification batch for tagged Slack users",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"workspace", workspaceID,
		"sent_count", sentCount,
		"failed_count", failedCount,
		"total_users", len(slackUsers))
}

// sendDMNotificationsToBlockedUsers sends immediate DM notifications to blocked GitHub users.
// This runs in a separate goroutine to avoid blocking event processing.
// Used when no channels were notified (performs GitHub->Slack mapping).
func (c *Coordinator) sendDMNotificationsToBlockedUsers(
	ctx context.Context, workspaceID, owner, repo string,
	prNumber int, uniqueUsers map[string]bool,
	event struct {
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
	},
	checkResult *turn.CheckResponse,
) {
	slog.Info("starting immediate DM notifications for blocked GitHub users",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"workspace", workspaceID,
		"github_user_count", len(uniqueUsers))

	domain := c.configManager.Domain(owner)
	sentCount := 0
	failedCount := 0

	for githubUser := range uniqueUsers {
		// Map GitHub user to Slack user
		slackUserID, err := c.userMapper.SlackHandle(ctx, githubUser, owner, domain)
		if err != nil || slackUserID == "" {
			slog.Debug("no Slack mapping for GitHub user, skipping",
				"github_user", githubUser,
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
				"error", err)
			failedCount++
			continue
		}

		// No channel tagging (ChannelID empty), so DM will be sent immediately
		err = c.sendPRNotification(ctx, dmNotificationRequest{
			UserID:      slackUserID,
			ChannelID:   "", // empty means immediate send
			ChannelName: "", // not applicable
			Owner:       owner,
			Repo:        repo,
			PRNumber:    prNumber,
			PRTitle:     event.PullRequest.Title,
			PRAuthor:    event.PullRequest.User.Login,
			PRURL:       event.PullRequest.HTMLURL,
			CheckResult: checkResult,
		})
		if err != nil {
			slog.Warn("failed to notify user",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
				"github_user", githubUser,
				"slack_user", slackUserID,
				"error", err)
			failedCount++
		} else {
			sentCount++
		}
	}

	slog.Info("completed immediate DM notifications for blocked GitHub users",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"workspace", workspaceID,
		"sent_count", sentCount,
		"failed_count", failedCount,
		"total_github_users", len(uniqueUsers))
}

// prUpdateInfo contains PR information for DM updates.
type prUpdateInfo struct {
	CheckResult *turn.CheckResponse
	Owner       string
	Repo        string
	PRTitle     string
	PRAuthor    string
	PRState     string
	PRURL       string
	PRNumber    int
}

// updateDMMessagesForPR updates existing DM messages when PR state changes.
// This is used by polling to ensure DMs are updated even when channel messages didn't change.
// Replaces the old updateDMMessagesForPR function with the new simplified system.
func (c *Coordinator) updateDMMessagesForPR(ctx context.Context, info prUpdateInfo) {
	prTitle := info.PRTitle
	prAuthor := info.PRAuthor
	prState := info.PRState
	checkResult := info.CheckResult
	// Determine which users to update based on PR state
	var slackUserIDs []string

	// For terminal states (merged/closed), update all users who received DMs
	if prState == "merged" || prState == "closed" {
		slackUserIDs = c.stateStore.ListDMUsers(ctx, info.PRURL)
		if len(slackUserIDs) == 0 {
			slog.Debug("no DM recipients found for merged/closed PR",
				"pr", fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.PRNumber),
				"pr_state", prState)
			return
		}
		slog.Info("updating DMs for merged/closed PR",
			"pr", fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.PRNumber),
			"pr_state", prState,
			"dm_recipients", len(slackUserIDs))
	} else {
		// For other states, update only users who are currently blocked
		if checkResult == nil || len(checkResult.Analysis.NextAction) == 0 {
			slog.Debug("no blocked users, skipping DM updates",
				"pr", fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.PRNumber))
			return
		}

		// Map GitHub users to Slack users
		domain := c.configManager.Domain(info.Owner)
		for githubUser := range checkResult.Analysis.NextAction {
			if githubUser == "_system" {
				continue
			}

			slackUserID, err := c.userMapper.SlackHandle(ctx, githubUser, info.Owner, domain)
			if err != nil || slackUserID == "" {
				slog.Debug("no Slack mapping for GitHub user, skipping",
					"github_user", githubUser,
					"pr", fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.PRNumber),
					"error", err)
				continue
			}
			slackUserIDs = append(slackUserIDs, slackUserID)
		}

		if len(slackUserIDs) == 0 {
			slog.Debug("no Slack users found for blocked GitHub users",
				"pr", fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.PRNumber))
			return
		}
	}

	// Update DM for each user using the unified sendPRNotification function
	// No channel info (updates are direct), so req.ChannelID and req.ChannelName are empty
	updatedCount := 0
	skippedCount := 0

	for _, slackUserID := range slackUserIDs {
		err := c.sendPRNotification(ctx, dmNotificationRequest{
			UserID:      slackUserID,
			ChannelID:   "", // empty for direct updates
			ChannelName: "", // not applicable
			Owner:       info.Owner,
			Repo:        info.Repo,
			PRNumber:    info.PRNumber,
			PRTitle:     prTitle,
			PRAuthor:    prAuthor,
			PRURL:       info.PRURL,
			CheckResult: checkResult,
		})
		if err != nil {
			slog.Warn("failed to update DM message",
				"user", slackUserID,
				"pr", fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.PRNumber),
				"error", err,
				"impact", "user sees stale PR state in DM")
			skippedCount++
		} else {
			updatedCount++
		}
	}

	if updatedCount > 0 {
		slog.Info("updated DM messages for PR state change",
			"pr", fmt.Sprintf("%s/%s#%d", info.Owner, info.Repo, info.PRNumber),
			"pr_state", prState,
			"updated", updatedCount,
			"skipped", skippedCount,
			"total_recipients", len(slackUserIDs))
	}
}
