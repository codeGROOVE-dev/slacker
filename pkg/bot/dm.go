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
//
//nolint:maintidx,revive,gocognit // This function coordinates all DM scenarios (queued/sent, update/create, delay logic) and benefits from being in one place
func (c *Coordinator) sendPRNotification(ctx context.Context, req dmNotificationRequest) error {
	// Lock per user+PR to prevent concurrent goroutines from sending duplicate DMs
	lockKey := req.UserID + ":" + req.PRURL
	lockValue, _ := c.dmLocks.LoadOrStore(lockKey, &sync.Mutex{})
	mu := lockValue.(*sync.Mutex) //nolint:errcheck,revive // Type assertion always succeeds - we control what's stored
	mu.Lock()
	defer mu.Unlock()

	prState := "unknown"
	if req.CheckResult != nil {
		prState = req.CheckResult.Analysis.WorkflowState
	}

	// Check if there's a queued (not-yet-sent) DM for this user+PR
	pendingDMs, err := c.stateStore.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		slog.Warn("failed to check for pending DMs",
			"user", req.UserID,
			"pr", req.PRURL,
			"error", err)
	}

	// Find ALL pending DMs for this user+PR (there may be multiple if user tagged in multiple channels)
	var matchingPendingDMs []*state.PendingDM
	for i := range pendingDMs {
		if pendingDMs[i].UserID == req.UserID && pendingDMs[i].PRURL == req.PRURL {
			matchingPendingDMs = append(matchingPendingDMs, &pendingDMs[i])
		}
	}

	// Use the first pending DM for decision-making (they should all have same state)
	var pendingDM *state.PendingDM
	if len(matchingPendingDMs) > 0 {
		pendingDM = matchingPendingDMs[0]
		// Log if we found duplicates (indicates user was tagged in multiple channels)
		if len(matchingPendingDMs) > 1 {
			slog.Info("found multiple queued DMs for same user+PR (user tagged in multiple channels)",
				"user", req.UserID,
				"pr", req.PRURL,
				"count", len(matchingPendingDMs))
		}
	}

	// If there's a queued DM, check if the user still needs to be notified
	if pendingDM != nil {
		// Check if user still has action to take (based on turnclient analysis)
		userStillBlocked := false
		if req.CheckResult != nil && req.CheckResult.Analysis.NextAction != nil {
			// Check if this GitHub user is in NextAction
			// We need to map Slack user back to GitHub user for this check
			// For now, check if ANY action is needed (conservative approach)
			userStillBlocked = len(req.CheckResult.Analysis.NextAction) > 0
		}

		// If user no longer blocked, cancel ALL queued DMs
		if !userStillBlocked {
			slog.Info("cancelling queued DMs - user no longer blocked",
				"user", req.UserID,
				"pr", req.PRURL,
				"old_state", pendingDM.PRState,
				"new_state", prState,
				"count", len(matchingPendingDMs))
			for _, dm := range matchingPendingDMs {
				if err := c.stateStore.RemovePendingDM(ctx, dm.ID); err != nil {
					slog.Warn("failed to remove pending DM",
						"user", req.UserID,
						"pr", req.PRURL,
						"dm_id", dm.ID,
						"error", err)
				}
			}
			return nil
		}

		// User still blocked - update the queued DM if state changed
		if pendingDM.PRState != prState {
			slog.Info("updating queued DM with new state",
				"user", req.UserID,
				"pr", req.PRURL,
				"old_state", pendingDM.PRState,
				"new_state", prState,
				"scheduled_send", pendingDM.SendAfter,
				"removing_duplicates", len(matchingPendingDMs))
			// Remove ALL old queued DMs and queue ONE new one with updated state
			for _, dm := range matchingPendingDMs {
				if err := c.stateStore.RemovePendingDM(ctx, dm.ID); err != nil {
					slog.Warn("failed to remove pending DM for update",
						"user", req.UserID,
						"pr", req.PRURL,
						"dm_id", dm.ID,
						"error", err)
				}
			}
			// Queue single new DM with updated state
			return c.queueDMForUser(ctx, req, prState, pendingDM.SendAfter)
		}
		// State unchanged, queued DM is still valid
		slog.Debug("DM already queued with same state",
			"user", req.UserID,
			"pr", req.PRURL,
			"state", prState,
			"queued_count", len(matchingPendingDMs))
		return nil
	}

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
					"old_state", func() string {
						if !exists || lastNotif.LastState == "" {
							return "none"
						}
						return lastNotif.LastState
					}(),
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
				SentAt: func() time.Time {
					if !exists || lastNotif.SentAt.IsZero() {
						return time.Now()
					}
					return lastNotif.SentAt
				}(),
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

			// Cancel any pending queued DMs for this user+PR
			// This handles the case where we updated immediately due to state change
			// while a delayed DM was still queued
			c.cancelPendingDMs(ctx, req.UserID, req.PRURL)

			return nil
		}
		// All updates failed - fall through to send new DM
	}

	// If we know a DM exists but couldn't find/update it, don't send duplicate
	// This prevents duplicate DMs when history search fails or Slack API is slow
	if exists {
		slog.Warn("DM exists but couldn't be located or updated - skipping to prevent duplicate",
			"user", req.UserID,
			"pr", req.PRURL,
			"last_state", lastNotif.LastState,
			"new_state", prState,
			"has_channel_id", lastNotif.ChannelID != "",
			"has_message_ts", lastNotif.MessageTS != "",
			"impact", "user may see stale state temporarily")
		return nil
	}

	// Path 2: Send new DM (check delay logic)
	shouldQueue, sendAfter := c.shouldDelayNewDM(ctx, req.UserID, req.ChannelID, req.ChannelName, req.Owner, req.Repo)

	if shouldQueue {
		// Cancel any existing pending DMs for this user+PR before queueing new one
		// This ensures we never have duplicate queued DMs (e.g., from multiple channels)
		c.cancelPendingDMs(ctx, req.UserID, req.PRURL)

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

	// Cancel any pending queued DMs for this user+PR
	// This handles the case where we sent immediately due to state change
	// while a delayed DM was still queued
	c.cancelPendingDMs(ctx, req.UserID, req.PRURL)

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
// Channel membership is determined by caller - if channelID is non-empty, user was in at least one channel.
func (c *Coordinator) shouldDelayNewDM(
	_ context.Context,
	userID, channelID, channelName string,
	owner, _ string,
) (bool, time.Time) {
	// If channelID is empty, user wasn't in any channel we notified - send immediately
	if channelID == "" {
		slog.Debug("user not in any channel, sending DM immediately",
			"user", userID)
		return false, time.Time{}
	}

	// User was in at least one channel - apply configured delay
	delayMinutes := c.configManager.ReminderDMDelay(owner, channelName)
	delay := time.Duration(delayMinutes) * time.Minute

	// If delay is 0, feature is disabled - send immediately even if user in channel
	if delay == 0 {
		slog.Debug("DM delay feature disabled, sending immediately",
			"user", userID,
			"owner", owner)
		return false, time.Time{}
	}

	// User is in channel - queue for delayed delivery
	sendAfter := time.Now().Add(delay)
	slog.Debug("user in channel, delaying DM",
		"user", userID,
		"delay_minutes", delayMinutes,
		"send_after", sendAfter)
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
		ID:            fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix()),
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
	// Don't save to DMMessage yet - the queued DM is the source of truth until it's sent
	// This way, if the state changes, we can update the queued DM instead of sending early
	return c.stateStore.QueuePendingDM(ctx, dm)
}

// cancelPendingDMs removes any queued DMs for a user+PR.
// Called after successfully sending or updating a DM to prevent duplicates.
func (c *Coordinator) cancelPendingDMs(ctx context.Context, userID, prURL string) {
	// Get all pending DMs (even future ones)
	pendingDMs, err := c.stateStore.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		slog.Warn("failed to check for pending DMs to cancel",
			"user", userID,
			"pr", prURL,
			"error", err)
		return
	}

	// Remove any that match this user+PR
	for i := range pendingDMs {
		if pendingDMs[i].UserID == userID && pendingDMs[i].PRURL == prURL {
			if err := c.stateStore.RemovePendingDM(ctx, pendingDMs[i].ID); err != nil {
				slog.Warn("failed to cancel pending DM",
					"user", userID,
					"pr", prURL,
					"dm_id", pendingDMs[i].ID,
					"error", err)
			} else {
				slog.Debug("cancelled pending DM after immediate send/update",
					"user", userID,
					"pr", prURL,
					"dm_id", pendingDMs[i].ID)
			}
		}
	}
}

// sendDMNotificationsToTaggedUsers sends DM notifications to Slack users who were tagged in channels.
// This runs in a separate goroutine to avoid blocking event processing.
// Decides per-user whether to send immediately or delay based on channel membership.
func (c *Coordinator) sendDMNotificationsToTaggedUsers(
	ctx context.Context, workspaceID, owner, repo string,
	prNumber int, taggedUsers map[string]UserTagInfo,
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
		"user_count", len(taggedUsers))

	sentCount := 0
	failedCount := 0
	delayedCount := 0

	for _, userInfo := range taggedUsers {
		// Determine delay based on channel membership from THIS event
		// If user is NOT in any channel we notified → send immediately
		// If user IS in at least one channel → apply configured delay
		var channelID string
		if userInfo.IsInAnyChannel {
			// User is in a channel - apply delay logic
			// We don't need the specific channel, just any channel ID to trigger delay
			// Use a placeholder to signal "user was in a channel"
			channelID = "delay"
			delayedCount++
		}
		// If IsInAnyChannel is false, channelID stays empty → immediate send

		err := c.sendPRNotification(ctx, dmNotificationRequest{
			UserID:      userInfo.UserID,
			ChannelID:   channelID, // "delay" or empty based on channel membership
			ChannelName: "",        // not used
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
				"slack_user", userInfo.UserID,
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
		"delayed_count", delayedCount,
		"failed_count", failedCount,
		"total_users", len(taggedUsers))
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
