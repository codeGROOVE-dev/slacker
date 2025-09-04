// Package bot implements the coordination logic between GitHub, Slack, and notifications.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	slackpkg "github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// minInt returns the minimum of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ThreadCache manages PR thread IDs for a workspace.
type ThreadCache struct {
	prThreads map[string]ThreadInfo // "owner/repo#123" -> thread info
	mu        sync.RWMutex
}

// ThreadInfo stores thread information for a PR.
type ThreadInfo struct {
	ThreadTS  string `json:"thread_ts"`
	ChannelID string `json:"channel_id"`
	LastState string `json:"last_state"`
}

// NewThreadCache creates a new thread cache.
func NewThreadCache() *ThreadCache {
	return &ThreadCache{
		prThreads: make(map[string]ThreadInfo),
	}
}

// Get retrieves thread info for a PR.
func (tc *ThreadCache) Get(prKey string) (ThreadInfo, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	info, exists := tc.prThreads[prKey]
	return info, exists
}

// Set stores thread info for a PR.
func (tc *ThreadCache) Set(prKey string, info ThreadInfo) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.prThreads[prKey] = info
}

// Update modifies thread info for a PR.
func (tc *ThreadCache) Update(prKey string, updateFn func(*ThreadInfo) bool) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if info, exists := tc.prThreads[prKey]; exists {
		if updateFn(&info) {
			tc.prThreads[prKey] = info
		}
	}
}

// Coordinator coordinates between GitHub, Slack, and notifications.
type Coordinator struct {
	slack         *slackpkg.Client
	github        *github.Client
	stateManager  *state.Manager
	configManager *config.Manager
	notifier      *notify.Manager
	sprinklerURL  string
	threadCache   *ThreadCache
}

// New creates a new bot coordinator.
func New(
	ctx context.Context,
	slackClient *slackpkg.Client,
	githubClient *github.Client,
	stateManager *state.Manager,
	configManager *config.Manager,
	notifier *notify.Manager,
	sprinklerURL string,
) *Coordinator {
	c := &Coordinator{
		slack:         slackClient,
		github:        githubClient,
		stateManager:  stateManager,
		configManager: configManager,
		notifier:      notifier,
		sprinklerURL:  sprinklerURL,
		threadCache:   NewThreadCache(),
	}

	// Set GitHub client in config manager.
	configManager.SetGitHubClient(githubClient.Client())

	// Get workspace info and set in config manager for validation.
	if teamInfo, err := slackClient.GetWorkspaceInfo(ctx); err == nil {
		// Use the team domain as workspace identifier
		workspaceName := teamInfo.Domain + ".slack.com"
		configManager.SetWorkspaceName(workspaceName)
		slog.Info("set workspace name for config validation", "workspace", workspaceName)
	} else {
		slog.Warn("failed to get workspace info, config validation disabled", "error", err)
	}

	return c
}

// findOrCreatePRThread finds an existing thread or creates a new one for a PR.
func (c *Coordinator) findOrCreatePRThread(ctx context.Context, channelID, owner, repo string, prNumber int, prState string, pullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL string `json:"html_url"`
},
) (string, error) {
	prKey := fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)

	slog.Debug("finding or creating PR thread",
		"pr", prKey,
		"channel", channelID,
		"pr_state", prState)

	// Check cache first
	if threadInfo, exists := c.threadCache.Get(prKey); exists && threadInfo.ChannelID == channelID {
		slog.Debug("found PR thread in cache",
			"pr", prKey,
			"thread_ts", threadInfo.ThreadTS,
			"channel", channelID,
			"cached_state", threadInfo.LastState)
		return threadInfo.ThreadTS, nil
	}

	// Search Slack for existing thread by this bot
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
	// Use a reasonable search window - last 30 days or creation time if available
	// TODO: Once we have PR creation date in the struct, use that instead
	searchFrom := time.Now().AddDate(0, 0, -30) // 30 days fallback
	threadTS, err := c.searchForPRThread(ctx, channelID, prURL, searchFrom)
	if err != nil {
		slog.Warn("failed to search for existing PR thread",
			"pr", prKey,
			"channel", channelID,
			"error", err)
		// Continue to create new thread
	} else if threadTS != "" {
		slog.Info("found existing PR thread via search",
			"pr", prKey,
			"thread_ts", threadTS,
			"channel", channelID)

		// Cache the found thread
		c.threadCache.Set(prKey, ThreadInfo{
			ThreadTS:  threadTS,
			ChannelID: channelID,
			LastState: prState,
		})
		return threadTS, nil
	}

	// Create new thread
	slog.Info("creating new PR thread",
		"pr", prKey,
		"channel", channelID,
		"pr_state", prState)

	newThreadTS, err := c.createPRThread(ctx, channelID, owner, repo, prNumber, pullRequest)
	if err != nil {
		return "", fmt.Errorf("failed to create PR thread: %w", err)
	}

	// Cache the new thread
	c.threadCache.Set(prKey, ThreadInfo{
		ThreadTS:  newThreadTS,
		ChannelID: channelID,
		LastState: prState,
	})

	slog.Info("created and cached new PR thread",
		"pr", prKey,
		"thread_ts", newThreadTS,
		"channel", channelID,
		"initial_state", prState)

	return newThreadTS, nil
}

// searchForPRThread searches for an existing PR thread in a channel using channel history.
// This approach uses channels:history permission instead of search:read which isn't available to bots.
// Note: This is more expensive than search API but works reliably with basic bot permissions.
// Results are cached by the calling code to minimize API calls.
func (c *Coordinator) searchForPRThread(ctx context.Context, channelID, prURL string, prCreatedAt time.Time) (string, error) {
	slog.Debug("searching for existing PR thread using channel history",
		"channel", channelID,
		"pr_url", prURL)

	// Get bot info to identify our messages
	botInfo, err := c.slack.GetBotInfo(ctx)
	if err != nil {
		slog.Warn("failed to get bot info, cannot search for existing threads",
			"channel", channelID,
			"error", err)
		// Return empty string to indicate no thread found, not an error
		return "", nil
	}

	// Search from PR creation date (more efficient than arbitrary 10 days)
	// Slack timestamps are in seconds since epoch
	prCreatedTimestamp := prCreatedAt.Unix()
	oldestTimestamp := strconv.FormatInt(prCreatedTimestamp, 10)

	slog.Debug("searching channel history for bot messages",
		"channel", channelID,
		"bot_user_id", botInfo.UserID,
		"oldest_timestamp", oldestTimestamp,
		"pr_created_at", prCreatedAt.Format(time.RFC3339),
		"looking_for_url", prURL)

	// Get channel history - limit to 1000 messages for performance
	history, err := c.slack.GetChannelHistory(ctx, channelID, oldestTimestamp, "", 1000)
	if err != nil {
		slog.Warn("failed to get channel history",
			"channel", channelID,
			"error", err)
		// Return empty string to indicate no thread found, not an error
		// This allows graceful fallback to creating new threads
		return "", nil
	}

	slog.Debug("retrieved messages from channel history",
		"channel", channelID,
		"messages_count", len(history.Messages),
		"search_from", prCreatedAt.Format(time.RFC3339),
		"oldest_timestamp", oldestTimestamp)

	// Look through messages for bot-posted threads containing the PR URL
	for i := range history.Messages {
		msg := &history.Messages[i]
		// Only check messages from our bot
		if msg.User != botInfo.UserID {
			continue
		}

		// Check if this message contains the PR URL
		if strings.Contains(msg.Text, prURL) {
			// Parse timestamp to calculate message age
			var messageAgeHours int
			if ts, err := strconv.ParseFloat(msg.Timestamp, 64); err == nil {
				messageAgeHours = int(time.Since(time.Unix(int64(ts), 0)).Hours())
			}

			slog.Info("found existing PR thread via channel history",
				"channel", channelID,
				"thread_ts", msg.Timestamp,
				"pr_url", prURL,
				"message_age_hours", messageAgeHours,
				"message_preview", msg.Text[:min(100, len(msg.Text))])
			return msg.Timestamp, nil
		}
	}

	slog.Debug("no existing PR thread found in channel history",
		"channel", channelID,
		"pr_url", prURL,
		"messages_searched", len(history.Messages),
		"bot_user_id", botInfo.UserID)

	return "", nil
}

// postStateChangeUpdate posts a follow-up message about a PR state change.
func (c *Coordinator) postStateChangeUpdate(ctx context.Context, channelID, threadTS, owner, repo string, prNumber int, oldState, newState string) error {
	if oldState == newState {
		return nil // No change
	}

	prKey := fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)

	// Create state change message
	stateEmoji := map[string]string{
		"test_tube":     "🧪", // tests running/pending
		"broken_heart":  "💔", // tests broken
		"hourglass":     "⏳", // waiting on review
		"carpentry_saw": "🪚", // approved but needs work
		"check":         "✅", // reviewed & approved
		"pray":          "🙏", // merged
		"face_palm":     "🤦", // closed but not merged
	}

	oldEmoji := stateEmoji[oldState]
	if oldEmoji == "" {
		oldEmoji = oldState
	}

	newEmoji := stateEmoji[newState]
	if newEmoji == "" {
		newEmoji = newState
	}

	message := fmt.Sprintf("🔄 **State changed**: %s → %s", oldEmoji, newEmoji)

	slog.Info("posting state change update",
		"pr", prKey,
		"channel", channelID,
		"thread_ts", threadTS,
		"old_state", oldState,
		"new_state", newState)

	err := c.slack.PostThreadReply(ctx, channelID, threadTS, message)
	if err != nil {
		return fmt.Errorf("failed to post state change update: %w", err)
	}

	// Update cache with new state
	c.threadCache.Update(prKey, func(info *ThreadInfo) bool {
		info.LastState = newState
		return true
	})

	return nil
}

// SprinklerMessage represents a message from sprinkler.
type SprinklerMessage struct {
	Type      string    `json:"type,omitempty"`      // Message type (e.g., "ping", "event")
	Event     string    `json:"event,omitempty"`     // GitHub event type
	Repo      string    `json:"repo,omitempty"`      // Repository name
	PRNumber  int       `json:"pr_number,omitempty"` // PR number extracted from URL
	URL       string    `json:"url,omitempty"`       // GitHub URL for reference
	Timestamp time.Time `json:"timestamp,omitempty"` // Event timestamp from sprinkler
}

// processEvent processes a GitHub webhook event.
func (c *Coordinator) processEvent(ctx context.Context, msg SprinklerMessage) error {
	// Skip empty messages (likely subscription confirmations or keepalives)
	if msg.Event == "" && msg.Repo == "" {
		slog.Debug("received empty message from sprinkler, likely acknowledgment")
		return nil
	}

	// Skip messages without repo information
	if msg.Repo == "" {
		slog.Debug("received message without repo", "event", msg.Event)
		return nil
	}

	slog.Info("processing event", "event", msg.Event, "repo", msg.Repo)

	// Parse repo owner and name.
	parts := strings.Split(msg.Repo, "/")
	if len(parts) != 2 {
		slog.Warn("invalid repo format", "repo", msg.Repo)
		return fmt.Errorf("invalid repo format: %s", msg.Repo)
	}
	owner := parts[0]
	repo := parts[1]

	if owner == "" || repo == "" {
		slog.Warn("empty owner or repo name", "owner", owner, "repo", repo)
		return errors.New("empty owner or repo name")
	}

	// Load config for this org if not already loaded.
	if _, exists := c.configManager.Config(owner); !exists {
		if err := c.configManager.LoadConfig(ctx, owner); err != nil {
			slog.Warn("failed to load config for org", "org", owner, "error", err)
		}
	}

	// Handle different event types.
	switch msg.Event {
	case "pull_request":
		c.handlePullRequestFromSprinkler(ctx, owner, repo, msg.PRNumber, msg.URL, msg.Timestamp)
	case "pull_request_review":
		c.handlePullRequestReviewFromSprinkler(ctx, owner, repo, msg.PRNumber, msg.URL, msg.Timestamp)
	case "check_run", "check_suite":
		// Parse to get PR number.
		// This is simplified - in production, we'd need to map commits to PRs.
		slog.Debug("received check event", "owner", owner, "repo", repo)
	case "push":
		// Check if this is a push to .codeGROOVE repo.
		if repo == ".codeGROOVE" {
			slog.Info("reloading config", "org", owner)
			if err := c.configManager.ReloadConfig(ctx, owner); err != nil {
				slog.Warn("failed to reload config", "error", err)
			}
		}
	default:
		slog.Debug("unhandled event type", "event", msg.Event)
	}

	return nil
}

// handlePullRequestEvent handles pull request events.
func (c *Coordinator) handlePullRequestEvent(ctx context.Context, owner, repo string, payload json.RawMessage) {
	slog.Debug("handling pull request event",
		"owner", owner,
		"repo", repo,
		"payload_size", len(payload),
		"payload_preview", string(payload[:min(len(payload), 200)]))

	var event struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			User   struct {
				Login string `json:"login"`
			} `json:"user"`
			HTMLURL string `json:"html_url"`
		} `json:"pull_request"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		slog.Error("failed to unmarshal PR event - invalid GitHub webhook payload",
			"owner", owner,
			"repo", repo,
			"error", err,
			"payload_size", len(payload),
			"payload_preview", string(payload[:min(len(payload), 500)]),
			"unmarshal_failure", true,
			"possible_causes", []string{"double-marshaled JSON", "invalid webhook format", "corrupted payload"})
		return
	}

	// Additional validation - check for completely empty structures
	if event.Action == "" {
		slog.Error("PR event missing action field - invalid GitHub webhook structure",
			"owner", owner,
			"repo", repo,
			"payload_preview", string(payload[:min(len(payload), 300)]))
		return
	}

	// Check that we have a pull_request object with basic required fields
	if event.PullRequest.Title == "" && event.PullRequest.HTMLURL == "" {
		slog.Error("PR event missing pull_request fields - webhook may be incomplete",
			"owner", owner,
			"repo", repo,
			"action", event.Action,
			"pr_has_title", event.PullRequest.Title != "",
			"pr_has_url", event.PullRequest.HTMLURL != "",
			"pr_has_user", event.PullRequest.User.Login != "")
		return
	}

	// Validate we got the essential data
	if event.Number == 0 && event.PullRequest.Number == 0 {
		slog.Error("PR event missing number - payload does not contain valid GitHub webhook data",
			"owner", owner,
			"repo", repo,
			"action", event.Action,
			"event_number", event.Number,
			"pr_number", event.PullRequest.Number,
			"payload_sample", string(payload[:min(len(payload), 300)]))
		return
	}

	// Use PR number from either location
	prNumber := event.Number
	if prNumber == 0 {
		prNumber = event.PullRequest.Number
	}

	slog.Debug("successfully parsed PR event",
		"owner", owner,
		"repo", repo,
		"pr_number", prNumber,
		"action", event.Action,
		"title", event.PullRequest.Title,
		"author", event.PullRequest.User.Login)

	slog.Info("PR event", "owner", owner, "repo", repo, "number", prNumber, "action", event.Action)

	// Get channels for this repo.
	channels := c.configManager.ChannelsForRepo(owner, repo)
	slog.Info("evaluating PR for channel notifications",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"action", event.Action,
		"title", event.PullRequest.Title,
		"author", event.PullRequest.User.Login,
		"configured_channels", len(channels),
		"channels", channels)

	if len(channels) == 0 {
		slog.Info("no channels configured for PR - skipping channel notifications",
			"owner", owner,
			"repo", repo,
			"pr_number", prNumber)
		return
	}

	// Get PR state.
	prState, blockedOn, err := c.github.PRState(ctx, owner, repo, prNumber)
	if err != nil {
		slog.Error("failed to get PR state - cannot process notifications",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
			"error", err)
		return
	}

	slog.Info("retrieved PR state for notification processing",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"state", prState,
		"blocked_on_users", len(blockedOn),
		"blocked_on", blockedOn)

	// For now, use a default workspace ID.
	// In production, this would map channels to workspaces.
	workspaceID := "default"

	// Update or create PR state.
	pr := &state.PRState{
		Owner:       owner,
		Repo:        repo,
		Number:      event.Number,
		Title:       event.PullRequest.Title,
		Author:      event.PullRequest.User.Login,
		State:       prState,
		BlockedOn:   blockedOn,
		LastUpdated: time.Now(),
	}

	// Check if we already have a thread for this PR.
	existingPR, exists := c.stateManager.PRState(workspaceID, owner, repo, event.Number)
	if exists {
		pr.ThreadTS = existingPR.ThreadTS
		pr.ChannelID = existingPR.ChannelID
	}

	// Always create/update threads in all configured channels
	slog.Info("processing PR for all configured channels",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"action", event.Action,
		"channels_to_process", len(channels),
		"channels", channels,
		"pr_state", prState)

	// Process each configured channel
	for _, channelName := range channels {
		// Resolve channel name to ID for API calls
		channelID := c.slack.ResolveChannelID(ctx, channelName)
		if channelID != channelName {
			slog.Debug("resolved channel name to ID", "name", channelName, "id", channelID)
		}

		oldState := ""

		// Check cache for existing thread info to get old state
		prKey := fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)
		if threadInfo, exists := c.threadCache.Get(prKey); exists && threadInfo.ChannelID == channelID {
			oldState = threadInfo.LastState
		}

		// Find or create thread for this PR in this channel
		threadTS, err := c.findOrCreatePRThread(ctx, channelID, owner, repo, prNumber, prState, event.PullRequest)
		if err != nil {
			slog.Error("failed to find or create PR thread",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
				"channel", channelID,
				"error", err,
				"will_continue_with_next_channel", true)
			continue
		}

		// Update PR state to use the first successful thread (for backwards compatibility)
		if pr.ThreadTS == "" {
			pr.ThreadTS = threadTS
			pr.ChannelID = channelID
		}

		// Update reactions for current state - only change if state actually changed
		if oldState != prState {
			slog.Debug("updating reactions for state change",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
				"channel_name", channelName,
				"channel_id", channelID,
				"thread_ts", threadTS,
				"old_state", oldState,
				"new_state", prState)

			if err := c.slack.UpdateReactionsWithPrevious(ctx, channelID, threadTS, oldState, prState); err != nil {
				slog.Error("failed to update reaction for PR state",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
					"channel_name", channelName,
					"channel_id", channelID,
					"thread_ts", threadTS,
					"old_state", oldState,
					"new_state", prState,
					"error", err)
			} else {
				slog.Debug("updated PR reaction successfully",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
					"channel_id", channelID,
					"thread_ts", threadTS,
					"old_state", oldState,
					"new_state", prState)
			}
		} else {
			slog.Debug("PR state unchanged, skipping reaction update",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
				"channel_id", channelID,
				"state", prState)
		}

		// Post state change message if state has changed
		if oldState != "" && oldState != prState {
			if err := c.postStateChangeUpdate(ctx, channelID, threadTS, owner, repo, prNumber, oldState, prState); err != nil {
				slog.Error("failed to post state change update",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
					"channel", channelID,
					"thread_ts", threadTS,
					"old_state", oldState,
					"new_state", prState,
					"error", err)
			} else {
				slog.Info("posted state change update",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
					"channel", channelID,
					"old_state", oldState,
					"new_state", prState)
			}
		}

		// Track that we notified users in this channel for DM delay logic
		c.stateManager.UpdateChannelNotification(workspaceID, owner, repo, prNumber)

		slog.Info("successfully processed PR in channel",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
			"channel", channelID,
			"thread_ts", threadTS,
			"action", event.Action,
			"pr_state", prState,
			"had_state_change", oldState != "" && oldState != prState)
	}

	// Save PR state.
	c.stateManager.SetPRState(workspaceID, pr)

	slog.Info("PR state updated and saved",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
		"workspace", workspaceID,
		"final_state", prState,
		"thread_ts", pr.ThreadTS,
		"channel", pr.ChannelID)

	// Check if we need to notify blocked users.
	if len(blockedOn) > 0 {
		slog.Info("processing blocked users for PR notifications",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
			"blocked_users_count", len(blockedOn),
			"blocked_users", blockedOn,
			"pr_state", prState)

		for _, userID := range blockedOn {
			// In production, map GitHub username to Slack user ID.
			// Then update their app home view.
			slog.Info("user is blocking PR - potential notification candidate",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
				"github_user", userID,
				"pr_state", prState,
				"pr_author", event.PullRequest.User.Login,
				"needs_slack_mapping", true)
			// Would call: c.updateUserHome(ctx, workspaceID, slackUserID)
			// Would also call: c.notifier.NotifyUser(ctx, workspaceID, slackUserID, pr.ChannelID, pr)
		}
	} else {
		slog.Info("no users blocking PR - no notifications needed",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, prNumber),
			"pr_state", prState)
	}
}

// handlePullRequestFromSprinkler handles pull request events from sprinkler by fetching PR data from GitHub API.
func (c *Coordinator) handlePullRequestFromSprinkler(ctx context.Context, owner, repo string, prNumber int, sprinklerURL string, eventTimestamp time.Time) {
	slog.Info("handling PR event from sprinkler using turnclient",
		"owner", owner,
		"repo", repo,
		"pr_number", prNumber,
		"sprinkler_url", sprinklerURL)

	// Get GitHub installation token for turnclient authentication
	githubToken := c.github.InstallationToken(ctx)
	if githubToken == "" {
		slog.Error("no GitHub token available for turnclient authentication",
			"owner", owner,
			"repo", repo,
			"pr_number", prNumber)
		return
	}

	// Create and authenticate turnclient
	turnClient, err := turn.NewClient("https://turn.ready-to-review.dev") // TODO: make configurable
	if err != nil {
		slog.Error("failed to create turnclient",
			"owner", owner,
			"repo", repo,
			"pr_number", prNumber,
			"error", err)
		return
	}

	// Set GitHub token for authentication
	turnClient.SetAuthToken(githubToken)

	// Use the owner (organization name) as the username for turnclient
	// This avoids the 403 error from trying to get the GitHub installation user
	// which requires user-level permissions that GitHub App integrations don't have
	botUsername := owner
	slog.Debug("using owner as username for turnclient",
		"bot_username", botUsername,
		"owner", owner)

	// Use the turnclient to check the PR - this gives us rich PR state analysis
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
	checkResult, err := turnClient.Check(ctx, prURL, botUsername, eventTimestamp)
	if err != nil {
		slog.Error("failed to check PR with turnclient",
			"owner", owner,
			"repo", repo,
			"pr_number", prNumber,
			"pr_url", prURL,
			"bot_username", botUsername,
			"error", err)
		return
	}

	slog.Debug("successfully fetched PR analysis from turnclient",
		"owner", owner,
		"repo", repo,
		"pr_number", prNumber,
		"pr_size", checkResult.PRState.Size,
		"unresolved_comments", checkResult.PRState.UnresolvedComments,
		"checks_state", fmt.Sprintf("%+v", checkResult.PRState.Checks),
		"last_activity", checkResult.PRState.LastActivity)

	// Fetch actual PR details from GitHub to get title and author
	// TODO: Once turnclient provides PR title and author, we can use that instead of this GitHub API call
	pr, err := c.github.PR(ctx, owner, repo, prNumber)
	if err != nil {
		slog.Error("failed to fetch PR details from GitHub",
			"owner", owner,
			"repo", repo,
			"pr_number", prNumber,
			"error", err)
		return
	}

	// Create a synthetic webhook event to reuse existing logic with real PR data
	event := struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Number  int    `json:"number"`
			Title   string `json:"title"`
			HTMLURL string `json:"html_url"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"pull_request"`
	}{
		Action: "synchronize", // Use synchronize as default for sprinkler notifications
		Number: prNumber,
		PullRequest: struct {
			Number  int    `json:"number"`
			Title   string `json:"title"`
			HTMLURL string `json:"html_url"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
		}{
			Number:  prNumber,
			Title:   pr.GetTitle(),
			HTMLURL: pr.GetHTMLURL(),
			User: struct {
				Login string `json:"login"`
			}{
				Login: pr.GetUser().GetLogin(),
			},
		},
	}

	// Convert to JSON payload to reuse existing handlePullRequestEvent logic
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("failed to marshal synthetic PR event",
			"owner", owner,
			"repo", repo,
			"pr_number", prNumber,
			"error", err)
		return
	}

	// Call the existing handler with the synthetic payload
	c.handlePullRequestEvent(ctx, owner, repo, payload)
}

// handlePullRequestReviewFromSprinkler handles PR review events from sprinkler.
func (c *Coordinator) handlePullRequestReviewFromSprinkler(ctx context.Context, owner, repo string, prNumber int, sprinklerURL string, eventTimestamp time.Time) {
	slog.Info("handling PR review event from sprinkler",
		"owner", owner,
		"repo", repo,
		"pr_number", prNumber,
		"sprinkler_url", sprinklerURL,
		"note", "review events not fully implemented yet")
	// TODO: Implement review event handling if needed
}

// handlePullRequestReviewEvent handles PR review events.
func (c *Coordinator) handlePullRequestReviewEvent(ctx context.Context, owner, repo string, payload json.RawMessage) {
	var event struct {
		Action string `json:"action"`
		Review struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
			State string `json:"state"`
		} `json:"review"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		slog.Warn("failed to unmarshal review event", "error", err)
		return
	}

	workspaceID := "default"
	pr, exists := c.stateManager.PRState(workspaceID, owner, repo, event.PullRequest.Number)
	if !exists {
		return
	}

	// Update thread with review status.
	if pr.ThreadTS != "" && event.Action == "submitted" {
		message := fmt.Sprintf("@%s reviewed the PR", event.Review.User.Login)
		switch event.Review.State {
		case "approved":
			message += " ✅"
		case "changes_requested":
			message += " 🔧"
		default:
			// Other review states (commented, dismissed, etc.)
			message += fmt.Sprintf(" (%s)", event.Review.State)
		}
		if err := c.slack.PostThreadReply(ctx, pr.ChannelID, pr.ThreadTS, message); err != nil {
			slog.Warn("failed to send thread update", "error", err)
		}
	}

	// Update PR state.
	prState, blockedOn, err := c.github.PRState(ctx, owner, repo, event.PullRequest.Number)
	if err == nil {
		oldState := pr.State
		pr.State = prState
		pr.BlockedOn = blockedOn
		pr.LastUpdated = time.Now()
		c.stateManager.SetPRState(workspaceID, pr)

		// Update reaction only if state changed.
		if pr.ThreadTS != "" && oldState != prState {
			slog.Debug("updating reaction for PR review state change",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.PullRequest.Number),
				"old_state", oldState,
				"new_state", prState,
				"review_action", event.Action)
			if err := c.slack.UpdateReactionsWithPrevious(ctx, pr.ChannelID, pr.ThreadTS, oldState, prState); err != nil {
				slog.Warn("failed to update reaction", "error", err)
			}
		}
	}
}

// createPRThread creates a new thread in Slack for a PR.
func (c *Coordinator) createPRThread(ctx context.Context, channel, owner, repo string, number int, pr struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL string `json:"html_url"`
},
) (string, error) {
	// Get prefix for this org.
	prefix := c.configManager.Prefix(owner)

	// Format message.
	text := fmt.Sprintf(
		"%s %s • <%s|%s#%d> by @%s",
		prefix,
		pr.Title,
		pr.HTMLURL,
		repo,
		number,
		pr.User.Login,
	)

	// Resolve channel name to ID for consistent API calls
	resolvedChannel := c.slack.ResolveChannelID(ctx, channel)
	if resolvedChannel != channel {
		slog.Debug("resolved channel for thread creation", "original", channel, "resolved", resolvedChannel)
	} else {
		slog.Debug("channel resolution did not change value", "channel", channel, "might_be_channel_id_already", resolvedChannel[0] == 'C')
	}

	// Create thread with resolved channel ID.
	threadTS, err := c.slack.PostThread(ctx, resolvedChannel, text, nil)
	if err != nil {
		return "", fmt.Errorf("failed to post thread: %w", err)
	}

	// Add initial reaction based on state.
	prState, _, err := c.github.PRState(ctx, owner, repo, number)
	if err == nil {
		slog.Debug("adding initial reaction for new thread",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, number),
			"channel", resolvedChannel,
			"thread_ts", threadTS,
			"pr_state", prState)
		if err := c.slack.UpdateReactions(ctx, resolvedChannel, threadTS, prState); err != nil {
			slog.Warn("failed to add initial reaction", "error", err, "channel", resolvedChannel, "thread_ts", threadTS, "pr_state", prState)
		}
	}

	return threadTS, nil
}
