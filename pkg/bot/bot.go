// Package bot implements the coordination logic between GitHub, Slack, and notifications.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// Common logging constants.
const (
	logFieldPR      = "pr"
	logFieldRepo    = "repo"
	logFieldOwner   = "owner"
	logFieldChannel = "channel"
	prFormatString  = "%s/%s#%d"
	historyPageSize = 1000
)

// prContext groups PR identification parameters to reduce function argument counts.
type prContext struct {
	CheckRes *turn.CheckResponse
	Event    any // The webhook event struct
	Owner    string
	Repo     string
	State    string
	Number   int
}

// messageUpdateParams groups parameters for message update operations.
type messageUpdateParams struct {
	CheckResult    *turn.CheckResponse
	Event          any
	ChannelID      string
	ChannelName    string
	ChannelDisplay string
	ThreadTS       string
	OldState       string
	CurrentText    string
	PRState        string
	Owner          string
	Repo           string
	PRNumber       int
}

// Coordinator coordinates between GitHub, Slack, and notifications for a single org.
//
//nolint:govet // Field order optimized for logical grouping over memory alignment
type Coordinator struct {
	processingEvents   sync.WaitGroup // Tracks in-flight event processing for graceful shutdown
	stateStore         StateStore     // Persistent state across restarts
	sprinklerURL       string
	workspaceName      string // Track workspace name for better logging
	slack              SlackClient
	github             GitHubClient
	configManager      ConfigManager
	notifier           *notify.Manager
	userMapper         UserMapper
	threadCache        *cache.ThreadCache   // In-memory cache for fast lookups
	commitPRCache      *cache.CommitPRCache // Maps commit SHAs to PR numbers for check events
	eventSemaphore     chan struct{}        // Limits concurrent event processing (prevents overwhelming APIs)
	dmLocks            sync.Map             // Per-user-PR locks to prevent duplicate DMs (key: "userID:prURL")
	recentUpdateLogsMu sync.Mutex           // Protects recentUpdateLogs map
	recentUpdateLogs   map[string]time.Time // Tracks recent "updating message" logs to prevent spam (key: "channelID:threadTS:state")
}

// StateStore interface for persistent state - allows dependency injection for testing.
//
//nolint:interfacebloat // 20 methods needed for complete state management
type StateStore interface {
	Thread(ctx context.Context, owner, repo string, number int, channelID string) (cache.ThreadInfo, bool)
	SaveThread(ctx context.Context, owner, repo string, number int, channelID string, info cache.ThreadInfo) error
	LastDM(ctx context.Context, userID, prURL string) (time.Time, bool)
	RecordDM(ctx context.Context, userID, prURL string, sentAt time.Time) error
	DMMessage(ctx context.Context, userID, prURL string) (state.DMInfo, bool)
	SaveDMMessage(ctx context.Context, userID, prURL string, info state.DMInfo) error
	ListDMUsers(ctx context.Context, prURL string) []string
	LastDigest(ctx context.Context, userID, date string) (time.Time, bool)
	RecordDigest(ctx context.Context, userID, date string, sentAt time.Time) error
	QueuePendingDM(ctx context.Context, dm *state.PendingDM) error
	PendingDMs(ctx context.Context, before time.Time) ([]state.PendingDM, error)
	RemovePendingDM(ctx context.Context, id string) error
	WasProcessed(ctx context.Context, eventKey string) bool
	MarkProcessed(ctx context.Context, eventKey string, ttl time.Duration) error
	LastNotification(ctx context.Context, prURL string) time.Time
	RecordNotification(ctx context.Context, prURL string, notifiedAt time.Time) error
	LastReportSent(ctx context.Context, userID string) (time.Time, bool)
	RecordReportSent(ctx context.Context, userID string, sentAt time.Time) error
	Cleanup(ctx context.Context) error
	Close() error
}

// New creates a new bot coordinator for a single GitHub organization.
func New(
	ctx context.Context,
	slackClient SlackClient,
	githubClient GitHubClient,
	configManager ConfigManager,
	notifier *notify.Manager,
	sprinklerURL string,
	stateStore StateStore,
) *Coordinator {
	c := &Coordinator{
		slack:            slackClient,
		github:           githubClient,
		configManager:    configManager,
		notifier:         notifier,
		userMapper:       usermapping.New(slackClient.API(), githubClient.InstallationToken(ctx)),
		sprinklerURL:     sprinklerURL,
		stateStore:       stateStore,
		threadCache:      cache.New(),
		commitPRCache:    cache.NewCommitPRCache(),
		eventSemaphore:   make(chan struct{}, 10), // Allow 10 concurrent events per org
		recentUpdateLogs: make(map[string]time.Time),
	}

	// Set GitHub client in config manager for this org.
	org := githubClient.Organization()
	if ghClient := githubClient.Client(); ghClient != nil {
		configManager.SetGitHubClient(org, ghClient)
	}

	// Get workspace info and set in config manager for validation.
	if teamInfo, err := slackClient.WorkspaceInfo(ctx); err == nil {
		// Use the team domain as workspace identifier
		workspaceName := teamInfo.Domain + ".slack.com"
		c.workspaceName = workspaceName
		configManager.SetWorkspaceName(workspaceName)
		slog.Info("initialized bot coordinator",
			"workspace", workspaceName,
			"workspace_id", teamInfo.ID,
			"workspace_domain", teamInfo.Domain,
			"ready_for_events", true)
	} else {
		slog.Warn("failed to get workspace info, config validation disabled", "error", err)
	}

	return c
}

// saveThread persists thread info to both cache and persistent storage.
// This ensures threads survive restarts and are available for closed PR updates.
func (c *Coordinator) saveThread(ctx context.Context, owner, repo string, number int, channelID string, info cache.ThreadInfo) {
	// Save to in-memory cache for fast lookups
	key := fmt.Sprintf("%s/%s#%d:%s", owner, repo, number, channelID)
	c.threadCache.Set(key, info)

	// Persist to state store for cross-instance sharing and restart recovery
	if err := c.stateStore.SaveThread(ctx, owner, repo, number, channelID, info); err != nil {
		slog.Warn("failed to persist thread to state store",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, number),
			"channel_id", channelID,
			"error", err,
			"impact", "thread updates may fail after restart")
	} else {
		slog.Debug("persisted thread to state store",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, number),
			"channel_id", channelID,
			"thread_ts", info.ThreadTS)
	}
}

// findOrCreatePRThread finds an existing thread or creates a new one for a PR.
// Returns (threadTS, wasNewlyCreated, currentMessageText, error).
//
//nolint:revive // Four return values needed to track thread state and creation status
func (c *Coordinator) findOrCreatePRThread(ctx context.Context, channelID, owner, repo string, prNumber int, prState string, pullRequest struct {
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Number  int    `json:"number"`
}, checkResult *turn.CheckResponse,
) (threadTS string, wasNewlyCreated bool, currentMessageText string, err error) {
	cacheKey := fmt.Sprintf("%s/%s#%d:%s", owner, repo, prNumber, channelID)

	slog.Debug("finding or creating PR thread",
		"pr", cacheKey,
		logFieldChannel, channelID,
		"pr_state", prState)

	// Try to find existing thread
	threadTS, messageText, found := c.findPRThread(ctx, cacheKey, channelID, owner, repo, prNumber, prState, pullRequest)
	if found {
		return threadTS, false, messageText, nil
	}

	// Thread not found - create new one with concurrent creation prevention
	threadInfo, wasCreated, err := c.createPRThreadWithLocking(ctx, channelID, owner, repo, prNumber, prState, pullRequest, checkResult)
	if err != nil {
		return "", false, "", err
	}

	return threadInfo.ThreadTS, wasCreated, threadInfo.MessageText, nil
}

// findPRThread searches for an existing PR thread in cache and Slack.
// Returns (threadTS, messageText, found).
func (c *Coordinator) findPRThread(
	ctx context.Context, cacheKey, channelID, owner, repo string, prNumber int, prState string,
	pullRequest struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	},
) (threadTS, messageText string, found bool) {
	// Check cache first
	if threadInfo, exists := c.threadCache.Get(cacheKey); exists {
		slog.Debug("found PR thread in cache",
			"pr", cacheKey,
			"thread_ts", threadInfo.ThreadTS,
			logFieldChannel, channelID,
			"cached_state", threadInfo.LastState,
			"has_cached_message_text", threadInfo.MessageText != "")
		return threadInfo.ThreadTS, threadInfo.MessageText, true
	}

	// Search Slack for existing thread
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
	searchFrom := pullRequest.CreatedAt
	if searchFrom.IsZero() || time.Since(searchFrom) > 30*24*time.Hour {
		searchFrom = time.Now().AddDate(0, 0, -30)
		slog.Debug("using 30-day fallback for thread search",
			"pr", cacheKey,
			"pr_created_at_available", !pullRequest.CreatedAt.IsZero(),
			"pr_age_days", int(time.Since(pullRequest.CreatedAt).Hours()/24))
	} else {
		slog.Debug("using PR creation date for thread search",
			"pr", cacheKey,
			"pr_created_at", searchFrom.Format(time.RFC3339),
			"search_window_days", int(time.Since(searchFrom).Hours()/24))
	}

	threadTS, messageText = c.searchForPRThread(ctx, channelID, prURL, searchFrom)
	if threadTS != "" {
		slog.Info("found existing PR thread via initial search",
			"pr", cacheKey,
			"thread_ts", threadTS,
			logFieldChannel, channelID,
			"current_message_preview", messageText[:min(100, len(messageText))])

		// Save the found thread
		c.saveThread(ctx, owner, repo, prNumber, channelID, cache.ThreadInfo{
			ThreadTS:    threadTS,
			ChannelID:   channelID,
			LastState:   prState,
			MessageText: messageText,
		})
		return threadTS, messageText, true
	}

	return "", "", false
}

// createPRThreadWithLocking creates a new PR thread with concurrent creation prevention.
// Returns (threadInfo, wasCreated, error).
// wasCreated is true if a new thread was created, false if an existing thread was found.
func (c *Coordinator) createPRThreadWithLocking(
	ctx context.Context, channelID, owner, repo string, prNumber int, prState string,
	pullRequest struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	},
	checkResult *turn.CheckResponse,
) (threadInfo cache.ThreadInfo, wasCreated bool, err error) {
	cacheKey := fmt.Sprintf("%s/%s#%d:%s", owner, repo, prNumber, channelID)
	// Prevent concurrent creation within this instance
	if !c.threadCache.MarkCreating(cacheKey) {
		// Wait for another goroutine to finish
		threadTS, messageText, shouldProceed := c.waitForConcurrentCreation(cacheKey)
		if !shouldProceed {
			// Thread was found - not newly created
			return cache.ThreadInfo{
				ThreadTS:    threadTS,
				MessageText: messageText,
			}, false, nil
		}
		// If shouldProceed is true, we've successfully marked as creating and should continue
	}

	// Double-check cache after acquiring lock
	if info, exists := c.threadCache.Get(cacheKey); exists {
		c.threadCache.UnmarkCreating(cacheKey)
		slog.Debug("found PR thread in cache after marking as creating",
			"pr", cacheKey,
			"thread_ts", info.ThreadTS)
		return info, false, nil
	}

	defer c.threadCache.UnmarkCreating(cacheKey)

	// Cross-instance race prevention check
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
	time.Sleep(100 * time.Millisecond)
	crossInstanceTS, crossInstanceText := c.searchForPRThread(ctx, channelID, prURL, pullRequest.CreatedAt)
	if crossInstanceTS != "" {
		slog.Info("found thread created by another instance (cross-instance race avoided)",
			"pr", cacheKey,
			"thread_ts", crossInstanceTS,
			logFieldChannel, channelID,
			"current_message_preview", crossInstanceText[:min(100, len(crossInstanceText))],
			"note", "this prevented duplicate thread creation during rolling deployment")

		info := cache.ThreadInfo{
			ThreadTS:    crossInstanceTS,
			ChannelID:   channelID,
			LastState:   prState,
			MessageText: crossInstanceText,
		}
		c.saveThread(ctx, owner, repo, prNumber, channelID, info)
		return info, false, nil
	}

	// Create new thread
	slog.Info("creating new PR thread",
		"pr", cacheKey,
		logFieldChannel, channelID,
		"pr_state", prState,
		"pr_created_at", pullRequest.CreatedAt.Format(time.RFC3339))

	newThreadTS, newMessageText, err := c.createPRThread(ctx, channelID, owner, repo, prNumber, prState, pullRequest, checkResult)
	if err != nil {
		return cache.ThreadInfo{}, false, fmt.Errorf("failed to create PR thread: %w", err)
	}

	// Save the new thread
	info := cache.ThreadInfo{
		ThreadTS:    newThreadTS,
		ChannelID:   channelID,
		LastState:   prState,
		MessageText: newMessageText,
	}
	c.saveThread(ctx, owner, repo, prNumber, channelID, info)

	slog.Info("created and cached new PR thread",
		"pr", cacheKey,
		"thread_ts", newThreadTS,
		logFieldChannel, channelID,
		"initial_state", prState,
		"message_preview", newMessageText[:min(100, len(newMessageText))],
		"creation_successful", true,
		"note", "if you see duplicate threads, check if another instance created one during the same time window")

	return info, true, nil
}

// waitForConcurrentCreation waits for another goroutine to finish creating the thread.
// Returns (threadTS, messageText, shouldProceed) where shouldProceed indicates whether caller should create.
func (c *Coordinator) waitForConcurrentCreation(cacheKey string) (threadTS, messageText string, shouldProceed bool) {
	slog.Info("another goroutine is creating this PR thread, waiting for completion", "pr", cacheKey)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)

		if threadInfo, exists := c.threadCache.Get(cacheKey); exists {
			slog.Info("found PR thread after waiting for concurrent creation",
				"pr", cacheKey,
				"thread_ts", threadInfo.ThreadTS,
				"waited", time.Since(time.Now().Add(-30*time.Second)))
			return threadInfo.ThreadTS, threadInfo.MessageText, false
		}

		// Check if the other goroutine finished
		if !c.threadCache.IsCreating(cacheKey) {
			// Other goroutine finished but didn't cache - try to create ourselves
			if c.threadCache.MarkCreating(cacheKey) {
				// Successfully marked - caller should proceed with creation
				return "", "", true
			}
			// Someone else marked it first, continue waiting
			continue
		}
	}

	// Timeout - try one last time to mark as creating
	if c.threadCache.IsCreating(cacheKey) {
		slog.Warn("timed out waiting for concurrent thread creation", "pr", cacheKey)
		if c.threadCache.MarkCreating(cacheKey) {
			return "", "", true
		}
	}

	// Failed to acquire lock - return empty to avoid creation
	return "", "", false
}

// searchForPRThread searches for an existing PR thread in a channel using channel history.
// This approach uses channels:history permission instead of search:read which isn't available to bots.
// Note: This is more expensive than search API but works reliably with basic bot permissions.
// Results are cached by the calling code to minimize API calls.
// Returns (threadTS, currentMessageText) - both empty if not found.
func (c *Coordinator) searchForPRThread(ctx context.Context, channelID, prURL string, prCreatedAt time.Time) (threadTS string, messageText string) {
	slog.Info("searching for existing PR thread using channel history",
		logFieldChannel, channelID,
		"pr_url", prURL)

	// Get bot info to identify our messages
	botInfo, err := c.slack.BotInfo(ctx)
	if err != nil {
		slog.Warn("failed to get bot info, cannot search for existing threads",
			logFieldChannel, channelID,
			"error", err)
		// Return empty strings to indicate no thread found
		return "", ""
	}

	// Search from PR creation date (more efficient than arbitrary 10 days)
	// Slack timestamps are in seconds since epoch
	ts := prCreatedAt.Unix()
	oldestTimestamp := strconv.FormatInt(ts, 10)

	slog.Debug("searching channel history for bot messages",
		logFieldChannel, channelID,
		"bot_user_id", botInfo.UserID,
		"oldest_timestamp", oldestTimestamp,
		"pr_created_at", prCreatedAt.Format(time.RFC3339),
		"looking_for_url", prURL)

	// Get channel history - limit to 1000 messages for performance
	history, err := c.slack.ChannelHistory(ctx, channelID, oldestTimestamp, "", historyPageSize)
	if err != nil {
		slog.Warn("failed to get channel history",
			logFieldChannel, channelID,
			"error", err)
		// Return empty strings to indicate no thread found
		// This allows graceful fallback to creating new threads
		return "", ""
	}

	slog.Info("retrieved messages from channel history for PR thread search",
		logFieldChannel, channelID,
		"messages_count", len(history.Messages),
		"search_from", prCreatedAt.Format(time.RFC3339),
		"oldest_timestamp", oldestTimestamp,
		"bot_user_id", botInfo.UserID,
		"searching_for_url", prURL)

	// Look through messages for bot-posted threads containing the PR URL
	// Note: We search for the base URL because posted messages may include state query params
	// like "?st=awaiting_review" which change as the PR progresses
	checked := 0
	for i := range history.Messages {
		msg := &history.Messages[i]
		// Only check messages from our bot
		if msg.User != botInfo.UserID {
			continue
		}
		checked++

		slog.Debug("checking bot message for PR URL",
			logFieldChannel, channelID,
			"message_ts", msg.Timestamp,
			"message_text", msg.Text,
			"looking_for", prURL,
			"contains_url", strings.Contains(msg.Text, prURL))

		// Check if this message contains the PR URL (base URL, before any query parameters)
		// The message may contain URLs like "https://github.com/org/repo/pull/32?st=awaiting_review"
		// but we search for the base "https://github.com/org/repo/pull/32"
		if strings.Contains(msg.Text, prURL) {
			// Parse timestamp to calculate message age
			var messageAgeHours int
			if ts, err := strconv.ParseFloat(msg.Timestamp, 64); err == nil {
				messageAgeHours = int(time.Since(time.Unix(int64(ts), 0)).Hours())
			}

			slog.Info("found existing PR thread via channel history",
				logFieldChannel, channelID,
				"thread_ts", msg.Timestamp,
				"pr_url", prURL,
				"message_age_hours", messageAgeHours,
				"message_preview", msg.Text[:min(100, len(msg.Text))])
			return msg.Timestamp, msg.Text
		}
	}

	slog.Info("no matching PR thread found in channel history",
		logFieldChannel, channelID,
		"pr_url", prURL,
		"total_messages_retrieved", len(history.Messages),
		"bot_messages_checked", checked,
		"bot_user_id", botInfo.UserID)

	return "", ""
}

// SprinklerMessage represents a message from sprinkler.
type SprinklerMessage struct {
	Timestamp time.Time `json:"timestamp,omitempty"` // Event timestamp from sprinkler
	Type      string    `json:"type,omitempty"`      // Message type (e.g., "ping", "event")
	Event     string    `json:"event,omitempty"`     // GitHub event type
	Repo      string    `json:"repo,omitempty"`      // Repository name
	URL       string    `json:"url,omitempty"`       // GitHub URL for reference
	PRNumber  int       `json:"pr_number,omitempty"` // PR number extracted from URL
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
		// Special handling for .codeGROOVE repo pull requests
		if repo == ".codeGROOVE" {
			slog.Info("received pull request event for .codeGROOVE repo",
				"org", owner,
				"pr", msg.PRNumber,
				"will_invalidate_cache_on_merge", true)
			// Note: Cache will be invalidated on push event when PR is merged
		}
		c.handlePullRequestFromSprinkler(ctx, owner, repo, msg.PRNumber, msg.URL, msg.Timestamp)
	case "pull_request_review":
		c.handlePullRequestReviewFromSprinkler(ctx, owner, repo, msg.PRNumber, msg.URL, msg.Timestamp)
	case "check_run", "check_suite":
		// Check events update PR test status - handle like pull_request events
		if msg.PRNumber > 0 {
			slog.Debug("received check event for PR, refreshing state",
				"owner", owner,
				"repo", repo,
				"pr", msg.PRNumber,
				"event", msg.Event)
			c.handlePullRequestFromSprinkler(ctx, owner, repo, msg.PRNumber, msg.URL, msg.Timestamp)
		} else {
			slog.Debug("received check event without PR number, skipping",
				"owner", owner,
				"repo", repo,
				"event", msg.Event,
				"url", msg.URL)
		}
	case "push":
		// Check if this is a push to .codeGROOVE repo.
		if repo == ".codeGROOVE" {
			slog.Info("reloading config due to push to .codeGROOVE repo",
				"org", owner,
				"invalidating_cache", true)
			if err := c.configManager.ReloadConfig(ctx, owner); err != nil {
				slog.Warn("failed to reload config", "error", err)
			}
		}
	default:
		slog.Debug("unhandled event type", "event", msg.Event)
	}

	return nil
}

// handlePullRequestEventWithData handles pull request events with pre-fetched data to avoid redundant API calls.
func (c *Coordinator) handlePullRequestEventWithData(ctx context.Context, owner, repo string, event struct {
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
}, checkResult *turn.CheckResponse, _ any,
) {
	prNumber := event.Number

	slog.Debug("PR event with pre-fetched data",
		logFieldOwner, owner,
		logFieldRepo, repo,
		"number", prNumber,
		"action", event.Action)

	// Load workspace and organization configuration
	if err := c.configManager.LoadConfig(ctx, owner); err != nil {
		slog.Error("failed to load config for org",
			"org", owner,
			"error", err)
		return
	}

	// Get workspace name from config for proper multi-workspace support
	workspaceID := c.configManager.WorkspaceName(owner)

	// Get channels for this PR
	channels := c.configManager.ChannelsForRepo(owner, repo)

	slog.Debug("evaluating PR for channel notifications",
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"action", event.Action,
		"title", event.PullRequest.Title,
		"author", event.PullRequest.User.Login,
		"configured_channels", len(channels),
		"channels", channels)

	if len(channels) == 0 {
		slog.Info("no channels configured for PR - skipping channel notifications",
			logFieldOwner, owner,
			logFieldRepo, repo,
			"pr_number", prNumber)
		return
	}

	// Extract state from turnclient response instead of making additional API calls
	prState := c.extractStateFromTurnclient(checkResult)
	blockedOn := c.extractBlockedUsersFromTurnclient(checkResult)

	slog.Debug("retrieved PR state for notification processing",
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"state", prState,
		"blocked_on_users", len(blockedOn),
		"blocked_on", blockedOn)

	// Process channels in parallel for better performance
	prCtx := prContext{
		Owner:    owner,
		Repo:     repo,
		Number:   prNumber,
		State:    prState,
		Event:    event,
		CheckRes: checkResult,
	}
	taggedUsers := c.processChannelsInParallel(ctx, prCtx, channels, workspaceID)

	// Handle user notifications - send DMs to blocked users
	// Logic:
	// - If channels were notified (taggedUsers not empty): Only send DMs to those successfully tagged Slack users
	// - If no channels were notified (taggedUsers empty/nil): Attempt DMs to all blocked GitHub users
	if len(blockedOn) > 0 {
		if len(taggedUsers) > 0 {
			// Channels were notified - only send DMs to successfully tagged Slack users
			slog.Info("preparing to send DM notifications to users tagged in channels",
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"total_blocked_github_users", len(blockedOn),
				"successfully_tagged_slack_users", len(taggedUsers),
				"will_send_async", true,
				"dm_logic", "immediate if not in channel, delayed if in channel")

			// Send DMs asynchronously to avoid blocking event processing
			// SECURITY NOTE: Use detached context to allow graceful completion of DM notifications
			// even if parent context is cancelled during shutdown. Operations are still bounded by
			// explicit 60-second timeout for DM delivery (~1s each).
			// Note: No panic recovery - we want panics to propagate and restart the service (Cloud Run will handle it)
			// A quiet failure is worse than a visible crash that triggers automatic recovery
			dmCtx, dmCancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			go func() {
				defer dmCancel()
				c.sendDMNotificationsToTaggedUsers(dmCtx, workspaceID, owner, repo, prNumber, taggedUsers, event, checkResult)
			}()
		} else {
			// No channels were notified - attempt to send immediate DMs to all blocked GitHub users
			// Deduplicate blocked users (same user might be blocked for multiple reasons)
			uniqueGitHubUsers := make(map[string]bool)
			for _, githubUser := range blockedOn {
				uniqueGitHubUsers[githubUser] = true
			}

			slog.Info("preparing to send immediate DMs (no channels were notified)",
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"total_blocked_github_users", len(blockedOn),
				"unique_github_users", len(uniqueGitHubUsers),
				"will_send_async", true,
				"warning", "DMs are sent async - if instance crashes before completion, another instance may retry and send duplicates")

			// Send DMs asynchronously to avoid blocking event processing
			// SECURITY NOTE: Use detached context to allow graceful completion of DM notifications
			// even if parent context is cancelled during shutdown. Operations are still bounded by
			// explicit 60-second timeout, allowing time for:
			// - GitHub email lookup via gh-mailto (~5-10s with retries/timeouts)
			// - Slack API user lookup for multiple guesses (~5-15s total)
			// - DM delivery (~1s)
			// This generous timeout prevents premature cancellation while still ensuring
			// eventual completion during shutdown. Most requests complete in <10s.
			// Note: No panic recovery - we want panics to propagate and restart the service (Cloud Run will handle it)
			// A quiet failure is worse than a visible crash that triggers automatic recovery
			dmCtx, dmCancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			go func() {
				defer dmCancel()
				c.sendDMNotificationsToBlockedUsers(dmCtx, workspaceID, owner, repo, prNumber, uniqueGitHubUsers, event, checkResult)
			}()
		}
	} else {
		slog.Info("no users blocking PR - no notifications needed",
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"pr_state", prState)

		// For merged/closed PRs, still update existing DMs even if no one is blocking
		// This ensures users see the final state (🚀 merged or ❌ closed)
		if prState == "merged" || prState == "closed" {
			slog.Info("updating DMs for terminal PR state",
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"pr_state", prState,
				"reason", "terminal_state_update")
			c.updateDMMessagesForPR(ctx, prUpdateInfo{
				Owner:       owner,
				Repo:        repo,
				PRNumber:    prNumber,
				PRTitle:     event.PullRequest.Title,
				PRAuthor:    event.PullRequest.User.Login,
				PRState:     prState,
				PRURL:       event.PullRequest.HTMLURL,
				CheckResult: checkResult,
			})
		}
	}
}

// extractStateFromTurnclient extracts PR state from turnclient response without additional API calls.
func (*Coordinator) extractStateFromTurnclient(checkResult *turn.CheckResponse) string {
	// Use turnclient's state analysis instead of making GitHub API calls
	// This maps turnclient states to our emoji reactions

	pr := checkResult.PullRequest
	analysis := checkResult.Analysis

	slog.Debug("extracting state from turnclient data",
		"pr_state", pr.State,
		"merged", pr.Merged,
		"merged_at", pr.MergedAt,
		"draft", pr.Draft,
		"ready_to_merge", analysis.ReadyToMerge,
		"approved", analysis.Approved,
		"checks_failing", analysis.Checks.Failing,
		"checks_pending", analysis.Checks.Pending,
		"checks_waiting", analysis.Checks.Waiting,
		"checks_passing", analysis.Checks.Passing,
		"unresolved_comments", analysis.UnresolvedComments,
		"tags", analysis.Tags)

	// Check if PR is merged (most direct way)
	if pr.Merged {
		slog.Info("PR detected as merged", "merged_at", pr.MergedAt)
		return "merged"
	}

	// Check if PR is closed but not merged
	if pr.State == "closed" {
		slog.Info("PR detected as closed but not merged", "state", "closed")
		return "closed"
	}

	if pr.Draft {
		slog.Debug("PR detected as draft", "state", "tests_running")
		return "tests_running" // Draft PRs show as tests running
	}

	if analysis.Checks.Failing > 0 {
		slog.Debug("PR has failing checks", "state", "tests_broken", "failing_count", analysis.Checks.Failing)
		return "tests_broken" // Tests failing
	}

	if analysis.Checks.Pending > 0 || analysis.Checks.Waiting > 0 {
		slog.Debug("PR has pending/waiting checks", "state", "tests_running",
			"pending_count", analysis.Checks.Pending,
			"waiting_count", analysis.Checks.Waiting)
		return "tests_running" // Tests running
	}

	if analysis.Approved {
		if analysis.UnresolvedComments > 0 {
			slog.Debug("PR approved but has unresolved comments", "state", "changes_requested",
				"unresolved_comments", analysis.UnresolvedComments)
			return "changes_requested" // Approved but has unresolved comments
		}
		slog.Debug("PR approved and ready", "state", "approved")
		return "approved" // Approved and ready
	}

	slog.Debug("PR waiting for review (default state)", "state", "awaiting_review")
	return "awaiting_review" // Default: waiting for review
}

// extractBlockedUsersFromTurnclient extracts blocked users from turnclient response.
func (*Coordinator) extractBlockedUsersFromTurnclient(checkResult *turn.CheckResponse) []string {
	var blockedUsers []string
	for user := range checkResult.Analysis.NextAction {
		// Skip _system sentinel value - it indicates processing state, not an actual user
		if user == "_system" {
			continue
		}
		blockedUsers = append(blockedUsers, user)
	}
	return blockedUsers
}

// formatNextActions formats NextAction map into a compact string like "fix tests: user1, user2; review: user3".
// It groups users by action kind and formats each action as "action_name: user1, user2".
// Multiple actions are separated by semicolons.
func (c *Coordinator) formatNextActions(ctx context.Context, checkResult *turn.CheckResponse, owner, domain string) string {
	if checkResult == nil || len(checkResult.Analysis.NextAction) == 0 {
		return ""
	}

	// Group users by action kind, filtering out _system users
	actionGroups := make(map[string][]string)
	for user, action := range checkResult.Analysis.NextAction {
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
		userMentions := c.userMapper.FormatUserMentions(ctx, users, owner, domain)

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

// getStateQueryParam returns the URL query parameter suffix for a given PR state.
func (*Coordinator) getStateQueryParam(prState string) string {
	switch prState {
	case "tests_running":
		return "?st=tests_running"
	case "tests_broken":
		return "?st=tests_broken"
	case "awaiting_review":
		return "?st=awaiting_review"
	case "changes_requested":
		return "?st=changes_requested"
	case "approved":
		return "?st=approved"
	default:
		return "" // No suffix for merged/closed - it's obvious from GitHub UI
	}
}

// UserTagInfo contains information about a user who was tagged in PR notifications.
type UserTagInfo struct {
	UserID         string
	IsInAnyChannel bool // True if user is member of at least one channel where they were tagged
}

// processChannelsInParallel processes multiple channels concurrently for better performance.
// processChannelsInParallel processes PR notifications for multiple channels concurrently.
// Returns a map of Slack user IDs to tag info for users successfully tagged in at least one channel.
func (c *Coordinator) processChannelsInParallel(
	ctx context.Context, prCtx prContext, channels []string, workspaceID string,
) map[string]UserTagInfo {
	event, ok := prCtx.Event.(struct {
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
	})
	if !ok {
		slog.Error("invalid event type in prContext", "expected", "pull_request_event")
		return nil
	}

	slog.Debug("processing PR for all configured channels",
		"workspace", c.workspaceName,
		logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
		"action", event.Action,
		"channels_to_process", len(channels),
		"channels", channels,
		"pr_state", prCtx.State)

	// Pre-filter channels to only those where the bot is a member (performance optimization)
	var validChannels []string
	for _, channelName := range channels {
		channelID := c.slack.ResolveChannelID(ctx, channelName)

		// Check if channel resolution failed (returns original name if not found)
		if channelID == channelName || (channelName != "" && channelName[0] == '#' && channelID == channelName[1:]) {
			slog.Warn("could not resolve channel - may not exist or bot lacks permissions",
				"workspace", c.workspaceName,
				logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
				"channel", channelName,
				"action_required", "verify channel exists and bot has access")
			continue
		}

		if c.slack.IsBotInChannel(ctx, channelID) {
			validChannels = append(validChannels, channelName)
		} else {
			slog.Warn("skipping channel - bot not a member",
				"workspace", c.workspaceName,
				logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
				"channel", channelName,
				"channel_id", channelID,
				"action_required", "invite bot to channel")
		}
	}

	if len(validChannels) == 0 {
		slog.Info("no valid channels to process - bot not in any configured channels",
			logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
			"total_channels", len(channels),
			"valid_channels", 0)
		return nil
	}

	slog.Debug("filtered channels for processing",
		logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
		"total_channels", len(channels),
		"valid_channels", len(validChannels),
		"filtered_out", len(channels)-len(validChannels))

	// Track which Slack users were successfully tagged across all channels
	var taggedUsersMu sync.Mutex
	taggedUsers := make(map[string]UserTagInfo)

	// Process channels in parallel for better performance
	// Use WaitGroup instead of errgroup since we don't want one failure to cancel others
	// Note: No panic recovery - we want panics to propagate and restart the service (Cloud Run will handle it)
	// A quiet failure is worse than a visible crash that triggers automatic recovery
	var wg sync.WaitGroup
	wg.Add(len(validChannels))

	for _, channelName := range validChannels {
		go func(ch string) {
			defer wg.Done()
			channelTaggedUsers := c.processPRForChannel(ctx, prCtx, ch, workspaceID)

			// Merge tagged users from this channel into the overall set
			taggedUsersMu.Lock()
			for userID, info := range channelTaggedUsers {
				existing, exists := taggedUsers[userID]
				if !exists {
					// First time seeing this user
					taggedUsers[userID] = info
				} else if info.IsInAnyChannel {
					// User already tagged in another channel - update IsInAnyChannel if this channel has them
					existing.IsInAnyChannel = true
					taggedUsers[userID] = existing
				}
			}
			taggedUsersMu.Unlock()
		}(channelName)
	}

	// Wait for all channels to complete
	wg.Wait()
	slog.Debug("completed parallel channel processing",
		logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
		"channels_processed", len(validChannels),
		"total_users_tagged", len(taggedUsers))

	return taggedUsers
}

// processPRForChannel handles PR processing for a single channel (extracted from the main loop).
// Returns a map of Slack user IDs to UserTagInfo for users successfully tagged in this channel.
func (c *Coordinator) processPRForChannel(
	ctx context.Context, prCtx prContext, channelName, workspaceID string,
) map[string]UserTagInfo {
	owner, repo, prNumber, prState := prCtx.Owner, prCtx.Repo, prCtx.Number, prCtx.State
	checkResult := prCtx.CheckRes
	event, ok := prCtx.Event.(struct {
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
	})
	if !ok {
		slog.Error("invalid event type in prContext", "expected", "pull_request_event", "channel", channelName)
		return nil
	}

	// Resolve and validate channel
	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, channelName, owner, repo, prNumber)
	if !ok {
		return nil
	}

	slog.Debug("processing PR for individual channel",
		"workspace", c.workspaceName,
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"channel", channelDisplay,
		"channel_id", channelID,
		"pr_state", prState,
		"action", event.Action)

	// Get old state from cache
	oldState := ""
	prKey := fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)
	if threadInfo, exists := c.threadCache.Get(prKey); exists && threadInfo.ChannelID == channelID {
		oldState = threadInfo.LastState
	}

	// Find or create thread
	pullRequestStruct := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		User:      event.PullRequest.User,
		HTMLURL:   event.PullRequest.HTMLURL,
		Title:     event.PullRequest.Title,
		Number:    event.PullRequest.Number,
		CreatedAt: event.PullRequest.CreatedAt,
	}
	threadTS, wasNewlyCreated, currentText, err := c.findOrCreatePRThread(
		ctx, channelID, owner, repo, prNumber, prState, pullRequestStruct, checkResult,
	)
	if err != nil {
		slog.Error("failed to find or create PR thread",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"channel", channelDisplay,
			"channel_id", channelID,
			"error", err,
			"impact", "channel_update_skipped_will_retry_via_polling",
			"next_poll_in", "5m",
			"will_continue_with_next_channel", true)
		return nil
	}

	// Track channel notification
	if c.notifier != nil && c.notifier.Tracker != nil {
		c.notifier.Tracker.UpdateChannelNotification(workspaceID, owner, repo, prNumber)
	}

	// Track user tags
	taggedUsers := c.trackUserTagsForDMDelay(ctx, workspaceID, channelID, channelDisplay, owner, repo, prNumber, checkResult)

	// Update message if needed
	if !wasNewlyCreated {
		c.updateMessageIfNeeded(ctx, messageUpdateParams{
			ChannelID:      channelID,
			ChannelName:    channelName,
			ChannelDisplay: channelDisplay,
			ThreadTS:       threadTS,
			OldState:       oldState,
			CurrentText:    currentText,
			PRState:        prState,
			Owner:          owner,
			Repo:           repo,
			PRNumber:       prNumber,
			Event:          event,
			CheckResult:    checkResult,
		})

		// For merged/closed PRs, ensure DM updates happen even if channel message didn't change
		// This handles the case where sprinkler already updated the channel but DMs weren't sent
		if prState == "merged" || prState == "closed" {
			c.updateDMMessagesForPR(ctx, prUpdateInfo{
				Owner:       owner,
				Repo:        repo,
				PRNumber:    prNumber,
				PRTitle:     event.PullRequest.Title,
				PRAuthor:    event.PullRequest.User.Login,
				PRState:     prState,
				PRURL:       event.PullRequest.HTMLURL,
				CheckResult: checkResult,
			})
		}
	}

	slog.Info("successfully processed PR in channel",
		"workspace", c.workspaceName,
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"channel", channelDisplay,
		"channel_id", channelID,
		"thread_ts", threadTS,
		"action", event.Action,
		"pr_state", prState,
		"had_state_change", oldState != "" && oldState != prState,
		"users_tagged", len(taggedUsers))

	return taggedUsers
}

// resolveAndValidateChannel resolves channel name to ID and creates display string.
// Returns (channelID, channelDisplay, ok).
func (c *Coordinator) resolveAndValidateChannel(
	ctx context.Context, channelName, owner, repo string, prNumber int,
) (channelID, channelDisplay string, ok bool) {
	channelID = c.slack.ResolveChannelID(ctx, channelName)
	if channelID == channelName || (channelName != "" && channelName[0] == '#' && channelID == channelName[1:]) {
		slog.Warn("could not resolve channel for PR processing",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"channel", channelName,
			"action_required", "verify channel exists and bot has access")
		return "", "", false
	}

	switch {
	case channelID != channelName:
		channelDisplay = fmt.Sprintf("#%s (%s)", channelName, channelID)
	case channelName != "" && channelName[0] == 'C':
		channelDisplay = channelID
	default:
		channelDisplay = fmt.Sprintf("#%s (unresolved)", channelName)
	}

	return channelID, channelDisplay, true
}

// trackUserTagsForDMDelay tracks user tags in channel for DM delay logic.
// Returns map of Slack user IDs to UserTagInfo with channel membership status.
func (c *Coordinator) trackUserTagsForDMDelay(
	ctx context.Context, workspaceID, channelID, channelDisplay, owner, repo string, prNumber int,
	checkResult *turn.CheckResponse,
) map[string]UserTagInfo {
	taggedUsers := make(map[string]UserTagInfo)
	blockedUsers := c.extractBlockedUsersFromTurnclient(checkResult)
	if len(blockedUsers) == 0 {
		return taggedUsers
	}

	domain := c.configManager.Domain(owner)
	// Record tags synchronously to prevent race with DM sending
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 30*time.Second)
	defer lookupCancel()

	for _, githubUser := range blockedUsers {
		slackUserID, err := c.userMapper.SlackHandle(lookupCtx, githubUser, owner, domain)
		if err == nil && slackUserID != "" {
			if c.notifier != nil && c.notifier.Tracker != nil {
				c.notifier.Tracker.UpdateUserPRChannelTag(workspaceID, slackUserID, channelID, owner, repo, prNumber)
			}

			// Check if user is member of this channel (for DM delay decision)
			isInChannel := c.slack.IsUserInChannel(ctx, channelID, slackUserID)

			taggedUsers[slackUserID] = UserTagInfo{
				UserID:         slackUserID,
				IsInAnyChannel: isInChannel,
			}

			slog.Debug("tracked user tag in channel",
				"workspace", workspaceID,
				"github_user", githubUser,
				"slack_user", slackUserID,
				"channel", channelDisplay,
				"channel_id", channelID,
				"is_in_channel", isInChannel,
				"pr", fmt.Sprintf(prFormatString, owner, repo, prNumber))
		}
	}

	return taggedUsers
}

// updateMessageIfNeeded builds the expected message and updates if different from current.
func (c *Coordinator) updateMessageIfNeeded(ctx context.Context, params messageUpdateParams) {
	event, ok := params.Event.(struct {
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
	})
	if !ok {
		slog.Error("invalid event type in messageUpdateParams", "expected", "pull_request_event")
		return
	}

	domain := c.configManager.Domain(params.Owner)
	msgParams := notify.MessageParams{
		CheckResult: params.CheckResult,
		Owner:       params.Owner,
		Repo:        params.Repo,
		PRNumber:    params.PRNumber,
		Title:       event.PullRequest.Title,
		Author:      event.PullRequest.User.Login,
		HTMLURL:     event.PullRequest.HTMLURL,
		Domain:      domain,
		ChannelName: params.ChannelName,
		UserMapper:  c.userMapper,
	}
	expectedText := notify.FormatChannelMessageBase(ctx, msgParams) + notify.FormatNextActionsSuffix(ctx, msgParams)

	if params.CurrentText == expectedText {
		slog.Debug("message already matches expected content, no update needed",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, params.Owner, params.Repo, params.PRNumber),
			"channel", params.ChannelDisplay,
			"thread_ts", params.ThreadTS,
			"pr_state", params.PRState)
		return
	}

	// Deduplicate "updating message" logs within 1 second to reduce noise from concurrent events
	logKey := fmt.Sprintf("%s:%s:%s", params.ChannelID, params.ThreadTS, params.PRState)
	shouldLog := true // Default to logging if map not initialized
	if c.recentUpdateLogs != nil {
		c.recentUpdateLogsMu.Lock()
		if lastLogged, exists := c.recentUpdateLogs[logKey]; !exists || time.Since(lastLogged) > time.Second {
			c.recentUpdateLogs[logKey] = time.Now()
			shouldLog = true
			// Clean up old entries (older than 5 seconds) to prevent memory leak
			for key, timestamp := range c.recentUpdateLogs {
				if time.Since(timestamp) > 5*time.Second {
					delete(c.recentUpdateLogs, key)
				}
			}
		} else {
			shouldLog = false
		}
		c.recentUpdateLogsMu.Unlock()
	}

	if shouldLog {
		slog.Info("updating message - content changed",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, params.Owner, params.Repo, params.PRNumber),
			"channel", params.ChannelDisplay,
			"channel_id", params.ChannelID,
			"thread_ts", params.ThreadTS,
			"pr_state", params.PRState,
			"old_state", params.OldState,
			"current_message_preview", params.CurrentText[:min(100, len(params.CurrentText))],
			"expected_message_preview", expectedText[:min(100, len(expectedText))])
	} else {
		slog.Debug("suppressing duplicate 'updating message' log (within 1s)",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, params.Owner, params.Repo, params.PRNumber),
			"channel", params.ChannelDisplay,
			"pr_state", params.PRState)
	}

	if err := c.slack.UpdateMessage(ctx, params.ChannelID, params.ThreadTS, expectedText); err != nil {
		slog.Error("failed to update PR message",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, params.Owner, params.Repo, params.PRNumber),
			"channel", params.ChannelDisplay,
			"channel_id", params.ChannelID,
			"thread_ts", params.ThreadTS,
			"error", err,
			"impact", "message_update_skipped_will_retry_via_polling",
			"next_poll_in", "5m")
		return
	}

	// Save updated thread info
	c.saveThread(ctx, params.Owner, params.Repo, params.PRNumber, params.ChannelID, cache.ThreadInfo{
		ThreadTS:    params.ThreadTS,
		ChannelID:   params.ChannelID,
		LastState:   params.PRState,
		MessageText: expectedText,
	})
	slog.Info("successfully updated PR message",
		"workspace", c.workspaceName,
		logFieldPR, fmt.Sprintf(prFormatString, params.Owner, params.Repo, params.PRNumber),
		"channel", params.ChannelDisplay,
		"channel_id", params.ChannelID,
		"thread_ts", params.ThreadTS,
		"pr_state", params.PRState)

	// Also update DM messages for blocked users
	c.updateDMMessagesForPR(ctx, prUpdateInfo{
		Owner:       params.Owner,
		Repo:        params.Repo,
		PRNumber:    params.PRNumber,
		PRTitle:     event.PullRequest.Title,
		PRAuthor:    event.PullRequest.User.Login,
		PRState:     params.PRState,
		PRURL:       event.PullRequest.HTMLURL,
		CheckResult: params.CheckResult,
	})
}

// handlePullRequestFromSprinkler handles pull request events from sprinkler by fetching PR data from GitHub API.
func (c *Coordinator) handlePullRequestFromSprinkler(
	ctx context.Context, owner, repo string, prNumber int, sprinklerURL string, eventTimestamp time.Time,
) {
	slog.Debug("handling PR event from sprinkler using turnclient",
		logFieldOwner, owner,
		logFieldRepo, repo,
		"pr_number", prNumber,
		"sprinkler_url", sprinklerURL)

	// Get GitHub installation token for turnclient authentication
	githubToken := c.github.InstallationToken(ctx)
	if githubToken == "" {
		slog.Error("no GitHub token available for turnclient authentication",
			logFieldOwner, owner,
			logFieldRepo, repo,
			"pr_number", prNumber)
		return
	}

	// Create and authenticate turnclient (allow test backend override for mocking)
	var turnClient *turn.Client
	var err error
	if testBackend := os.Getenv("TURN_TEST_BACKEND"); testBackend != "" {
		turnClient, err = turn.NewClient(testBackend)
	} else {
		turnClient, err = turn.NewDefaultClient()
	}
	if err != nil {
		slog.Error("failed to create turnclient",
			logFieldOwner, owner,
			logFieldRepo, repo,
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
		logFieldOwner, owner)

	// Use the turnclient to check the PR - this gives us rich PR state analysis
	// Add timeout to prevent hanging on slow API calls
	checkCtx, checkCancel := context.WithTimeout(ctx, 30*time.Second)
	defer checkCancel()

	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
	checkResult, err := turnClient.Check(checkCtx, prURL, botUsername, eventTimestamp)
	if err != nil {
		slog.Error("failed to check PR with turnclient",
			logFieldOwner, owner,
			logFieldRepo, repo,
			"pr_number", prNumber,
			"pr_url", prURL,
			"bot_username", botUsername,
			"error", err,
			"timeout_seconds", 30)
		return
	}

	slog.Debug("successfully fetched PR analysis from turnclient",
		logFieldOwner, owner,
		logFieldRepo, repo,
		"pr_number", prNumber,
		"pr_state", checkResult.PullRequest.State,
		"pr_draft", checkResult.PullRequest.Draft,
		"pr_merged", checkResult.PullRequest.Merged,
		"pr_size", checkResult.Analysis.Size,
		"unresolved_comments", checkResult.Analysis.UnresolvedComments,
		"checks_state", fmt.Sprintf("%+v", checkResult.Analysis.Checks),
		"last_activity", checkResult.Analysis.LastActivity)

	// Use PR details from turnclient instead of making additional GitHub API call
	pr := checkResult.PullRequest

	// Populate commit→PR cache for all commits in this PR
	// This allows us to quickly map check events (which only have commit SHA) back to PRs
	// without making expensive GitHub API calls
	if len(pr.Commits) > 0 {
		slog.Debug("populating commit→PR cache from turnclient response",
			logFieldOwner, owner,
			logFieldRepo, repo,
			"pr_number", prNumber,
			"commit_count", len(pr.Commits))

		for _, commitSHA := range pr.Commits {
			if commitSHA != "" {
				c.commitPRCache.RecordPR(owner, repo, prNumber, commitSHA)
			}
		}
	}

	// Create a synthetic webhook event to reuse existing logic with real PR data
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
		Action: "synchronize", // Use synchronize as default for sprinkler notifications
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
			Number: prNumber,
		},
		Number: prNumber,
	}

	// Call optimized handler with pre-fetched data to avoid redundant API calls
	c.handlePullRequestEventWithData(ctx, owner, repo, event, checkResult, nil)
}

// handlePullRequestReviewFromSprinkler handles PR review events from sprinkler.
// Reviews update PR state (approved, changes requested, etc), so we treat them
// like regular pull_request events and let turnclient analyze the current state.
func (c *Coordinator) handlePullRequestReviewFromSprinkler(
	ctx context.Context, owner, repo string, prNumber int, sprinklerURL string, eventTimestamp time.Time,
) {
	slog.Info("handling PR review event from sprinkler",
		logFieldOwner, owner,
		logFieldRepo, repo,
		"pr_number", prNumber,
		"sprinkler_url", sprinklerURL,
		"note", "treating as pull_request event - turnclient will analyze review state")

	// Reviews change PR state, so handle like any other PR update
	c.handlePullRequestFromSprinkler(ctx, owner, repo, prNumber, sprinklerURL, eventTimestamp)
}

// createPRThread creates a new thread in Slack for a PR.
// Critical performance optimization: Posts thread immediately WITHOUT user mentions,
// then updates asynchronously once email lookups complete (which take 13-20 seconds each).
func (c *Coordinator) createPRThread(ctx context.Context, channel, owner, repo string, number int, _ string, pr struct {
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Number  int    `json:"number"`
}, checkResult *turn.CheckResponse,
) (threadTS string, messageText string, err error) {
	// Format initial message WITHOUT user mentions (fast path)
	// Format: :emoji: Title repo#123 · author
	domain := c.configManager.Domain(owner)
	params := notify.MessageParams{
		CheckResult: checkResult,
		Owner:       owner,
		Repo:        repo,
		PRNumber:    number,
		Title:       pr.Title,
		Author:      pr.User.Login,
		HTMLURL:     pr.HTMLURL,
		Domain:      domain,
		UserMapper:  c.userMapper,
	}
	initialText := notify.FormatChannelMessageBase(ctx, params)

	// Resolve channel name to ID for consistent API calls
	resolvedChannel := c.slack.ResolveChannelID(ctx, channel)

	// Check if channel resolution failed (returns original name if not found)
	if resolvedChannel == channel || (channel != "" && channel[0] == '#' && resolvedChannel == channel[1:]) {
		// Only warn if it's not already a channel ID
		if channel == "" || channel[0] != 'C' {
			slog.Warn("could not resolve channel for thread creation",
				"workspace", c.workspaceName,
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, number),
				"channel", channel,
				"action_required", "verify channel exists and bot has access")
			return "", "", fmt.Errorf("could not resolve channel: %s", channel)
		}
		slog.Debug("channel is already a channel ID", "channel_id", channel)
	} else {
		slog.Debug("resolved channel for thread creation", "original", channel, "resolved", resolvedChannel)
	}

	// Create thread with resolved channel ID - post immediately without waiting for user lookups
	threadTS, err = c.slack.PostThread(ctx, resolvedChannel, initialText, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to post thread: %w", err)
	}

	slog.Info("thread created immediately (async user mention enrichment pending)",
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, number),
		logFieldChannel, resolvedChannel,
		"thread_ts", threadTS,
		"message_preview", initialText[:min(100, len(initialText))],
		"will_update_with_mentions", true)

	// Asynchronously add user mentions once email lookups complete
	// This avoids blocking thread creation on slow email lookups (13-20 seconds each)
	if checkResult != nil && len(checkResult.Analysis.NextAction) > 0 {
		// SECURITY NOTE: Use detached context to complete message enrichment even during shutdown.
		// Operations bounded by 60-second timeout, allowing time for:
		// - GitHub email lookup via gh-mailto (~5-10s with retries/timeouts)
		// - Slack API user lookups for multiple users (~5-15s total)
		// - Message update API call (~1s)
		// Most lookups hit cache instantly, but cold lookups can take 10+ seconds.
		enrichCtx, enrichCancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		// Capture variables to avoid data race
		capturedThreadTS := threadTS
		capturedOwner := owner
		capturedRepo := repo
		capturedNumber := number
		capturedChannel := resolvedChannel
		capturedInitialText := initialText
		capturedParams := params
		go func() {
			defer enrichCancel()

			// Perform email lookups in background
			nextActionsSuffix := notify.FormatNextActionsSuffix(enrichCtx, capturedParams)
			if nextActionsSuffix != "" {
				// Update message with user mentions
				enrichedText := capturedInitialText + nextActionsSuffix
				if err := c.slack.UpdateMessage(enrichCtx, capturedChannel, capturedThreadTS, enrichedText); err != nil {
					slog.Warn("failed to update thread with user mentions (async enrichment)",
						logFieldPR, fmt.Sprintf(prFormatString, capturedOwner, capturedRepo, capturedNumber),
						logFieldChannel, capturedChannel,
						"thread_ts", capturedThreadTS,
						"error", err)
				} else {
					slog.Info("thread enriched with user mentions (async)",
						logFieldPR, fmt.Sprintf(prFormatString, capturedOwner, capturedRepo, capturedNumber),
						logFieldChannel, capturedChannel,
						"thread_ts", capturedThreadTS,
						"next_actions_suffix", nextActionsSuffix)
				}
			}
		}()
	}

	return threadTS, initialText, nil
}
