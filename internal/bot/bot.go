// Package bot implements the coordination logic between GitHub, Slack, and notifications.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/config"
	"github.com/codeGROOVE-dev/slacker/internal/github"
	"github.com/codeGROOVE-dev/slacker/internal/notify"
	slackpkg "github.com/codeGROOVE-dev/slacker/internal/slack"
	"github.com/codeGROOVE-dev/slacker/internal/state"
	"github.com/codeGROOVE-dev/slacker/internal/usermapping"
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

// ThreadCache manages PR thread IDs for a workspace.
type ThreadCache struct {
	prThreads    map[string]ThreadInfo // "owner/repo#123" -> thread info
	mu           sync.RWMutex
	creationLock sync.Mutex      // Prevents concurrent creation of the same PR thread
	creating     map[string]bool // Track PRs currently being created
}

// ThreadInfo is an alias to state.ThreadInfo to avoid duplication.
type ThreadInfo = state.ThreadInfo

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
	info.UpdatedAt = time.Now()
	tc.prThreads[prKey] = info
}

// Cleanup removes entries older than the specified age.
// This prevents unbounded memory growth for closed/merged PRs.
func (tc *ThreadCache) Cleanup(maxAge time.Duration) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, info := range tc.prThreads {
		if info.UpdatedAt.Before(cutoff) {
			delete(tc.prThreads, key)
		}
	}
}

// Coordinator coordinates between GitHub, Slack, and notifications for a single org.
type Coordinator struct {
	slack             *slackpkg.Client
	github            *github.Client
	configManager     *config.Manager
	notifier          *notify.Manager
	userMapper        *usermapping.Service
	sprinklerURL      string
	threadCache       *ThreadCache         // In-memory cache for fast lookups
	stateStore        StateStore           // Persistent state across restarts
	workspaceName     string               // Track workspace name for better logging
	processedEvents   map[string]time.Time // In-memory event deduplication: "timestamp:url:type" -> processed time
	processedEventMu  sync.RWMutex
	processingEvents  map[string]bool // Track events currently being processed (prevents concurrent duplicates)
	processingEventMu sync.Mutex
	eventSemaphore    chan struct{} // Limits concurrent event processing (prevents overwhelming APIs)
}

// StateStore interface for persistent state - allows dependency injection for testing.
type StateStore interface {
	GetThread(owner, repo string, number int, channelID string) (ThreadInfo, bool)
	SaveThread(owner, repo string, number int, channelID string, info ThreadInfo) error
	GetLastDM(userID, prURL string) (time.Time, bool)
	RecordDM(userID, prURL string, sentAt time.Time) error
	WasProcessed(eventKey string) bool
	MarkProcessed(eventKey string, ttl time.Duration) error
	GetLastNotification(prURL string) time.Time
	RecordNotification(prURL string, notifiedAt time.Time) error
	Close() error
}

// New creates a new bot coordinator for a single GitHub organization.
func New(
	ctx context.Context,
	slackClient *slackpkg.Client,
	githubClient *github.Client,
	configManager *config.Manager,
	notifier *notify.Manager,
	sprinklerURL string,
	stateStore StateStore,
) *Coordinator {
	c := &Coordinator{
		slack:         slackClient,
		github:        githubClient,
		configManager: configManager,
		notifier:      notifier,
		userMapper:    usermapping.New(slackClient.API(), githubClient.InstallationToken(ctx)),
		sprinklerURL:  sprinklerURL,
		stateStore:    stateStore,
		threadCache: &ThreadCache{
			prThreads: make(map[string]ThreadInfo),
			creating:  make(map[string]bool),
		},
		processedEvents:  make(map[string]time.Time),
		processingEvents: make(map[string]bool),
		eventSemaphore:   make(chan struct{}, 10), // Allow 10 concurrent events per org
	}

	// Set GitHub client in config manager for this org.
	org := githubClient.Organization()
	configManager.SetGitHubClient(org, githubClient.Client())

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

// findOrCreatePRThread finds an existing thread or creates a new one for a PR.
// Returns (threadTS, wasNewlyCreated, error).
func (c *Coordinator) findOrCreatePRThread(ctx context.Context, channelID, owner, repo string, prNumber int, prState string, pullRequest struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL   string    `json:"html_url"`
	Title     string    `json:"title"`
	Number    int       `json:"number"`
	CreatedAt time.Time `json:"created_at"`
}, checkResult *turn.CheckResponse,
) (threadTS string, wasNewlyCreated bool, currentMessageText string, err error) {
	// Use cache key that includes channel ID to support multiple channels per PR
	cacheKey := fmt.Sprintf("%s/%s#%d:%s", owner, repo, prNumber, channelID)

	slog.Debug("finding or creating PR thread",
		"pr", cacheKey,
		logFieldChannel, channelID,
		"pr_state", prState)

	// Check cache first (quick read lock)
	if threadInfo, exists := c.threadCache.Get(cacheKey); exists {
		slog.Debug("found PR thread in cache",
			"pr", cacheKey,
			"thread_ts", threadInfo.ThreadTS,
			logFieldChannel, channelID,
			"cached_state", threadInfo.LastState,
			"has_cached_message_text", threadInfo.MessageText != "")
		return threadInfo.ThreadTS, false, threadInfo.MessageText, nil
	}

	// Not in cache - search Slack for existing thread before trying to create
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
	searchFrom := pullRequest.CreatedAt
	if searchFrom.IsZero() || time.Since(searchFrom) > 30*24*time.Hour {
		searchFrom = time.Now().AddDate(0, 0, -30) // 30 days fallback
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

	initialSearchTS, initialSearchText := c.searchForPRThread(ctx, channelID, prURL, searchFrom)
	if initialSearchTS != "" {
		slog.Info("found existing PR thread via initial search",
			"pr", cacheKey,
			"thread_ts", initialSearchTS,
			logFieldChannel, channelID,
			"current_message_preview", initialSearchText[:min(100, len(initialSearchText))])

		// Cache the found thread with its current message text
		c.threadCache.Set(cacheKey, ThreadInfo{
			ThreadTS:    initialSearchTS,
			ChannelID:   channelID,
			LastState:   prState,
			MessageText: initialSearchText,
		})
		return initialSearchTS, false, initialSearchText, nil
	}

	// Prevent concurrent creation of the same PR thread in same channel
	// Lock on cacheKey (with channel) to allow parallel creation in different channels
	c.threadCache.creationLock.Lock()
	// Check if another goroutine is already creating this thread in this channel
	if c.threadCache.creating[cacheKey] {
		c.threadCache.creationLock.Unlock()
		// Wait for the other goroutine to finish (up to 30 seconds)
		slog.Info("another goroutine is creating this PR thread, waiting for completion",
			"pr", cacheKey)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			if threadInfo, exists := c.threadCache.Get(cacheKey); exists {
				slog.Info("found PR thread after waiting for concurrent creation",
					"pr", cacheKey,
					"thread_ts", threadInfo.ThreadTS,
					"waited", time.Since(time.Now().Add(-30*time.Second)))
				return threadInfo.ThreadTS, false, "", nil
			}
			// Check if the other goroutine finished (even if it failed)
			c.threadCache.creationLock.Lock()
			stillCreating := c.threadCache.creating[cacheKey]
			c.threadCache.creationLock.Unlock()
			if !stillCreating {
				// Other goroutine finished but didn't cache (likely failed)
				// Proceed to try creating ourselves
				break
			}
		}
		slog.Warn("timed out waiting for concurrent thread creation, will try creating ourselves",
			"pr", cacheKey)
		c.threadCache.creationLock.Lock()
	}
	// Double-check cache while holding lock (another goroutine might have just finished)
	if threadInfo, exists := c.threadCache.Get(cacheKey); exists {
		c.threadCache.creationLock.Unlock()
		slog.Debug("found PR thread in cache during lock acquisition",
			"pr", cacheKey,
			"thread_ts", threadInfo.ThreadTS)
		return threadInfo.ThreadTS, false, "", nil
	}
	// Mark as creating
	c.threadCache.creating[cacheKey] = true
	c.threadCache.creationLock.Unlock()

	// Ensure we clean up the creating flag
	defer func() {
		c.threadCache.creationLock.Lock()
		delete(c.threadCache.creating, cacheKey)
		c.threadCache.creationLock.Unlock()
	}()

	// CRITICAL: Perform one final cross-instance check RIGHT before the expensive operations
	// This handles the case where another instance (during rolling deployment) just created
	// a thread while we were acquiring the lock. The creating flag only prevents races within
	// this instance - we need to check Slack itself to catch threads from other instances.
	// Add a small delay to let any concurrent creates from other instances complete their Slack API call.
	time.Sleep(100 * time.Millisecond)
	crossInstanceCheckTS, crossInstanceText := c.searchForPRThread(ctx, channelID, prURL, pullRequest.CreatedAt)
	if crossInstanceCheckTS != "" {
		slog.Info("found thread created by another instance (cross-instance race avoided)",
			"pr", cacheKey,
			"thread_ts", crossInstanceCheckTS,
			logFieldChannel, channelID,
			"current_message_preview", crossInstanceText[:min(100, len(crossInstanceText))],
			"note", "this prevented duplicate thread creation during rolling deployment")

		// Cache it and return
		c.threadCache.Set(cacheKey, ThreadInfo{
			ThreadTS:    crossInstanceCheckTS,
			ChannelID:   channelID,
			LastState:   prState,
			MessageText: crossInstanceText,
		})
		return crossInstanceCheckTS, false, crossInstanceText, nil
	}

	// Create new thread
	slog.Info("creating new PR thread",
		"pr", cacheKey,
		logFieldChannel, channelID,
		"pr_state", prState,
		"pr_created_at", pullRequest.CreatedAt.Format(time.RFC3339),
		"search_window_used", searchFrom.Format(time.RFC3339))

	newThreadTS, newMessageText, err := c.createPRThread(ctx, channelID, owner, repo, prNumber, prState, pullRequest, checkResult)
	if err != nil {
		return "", false, "", fmt.Errorf("failed to create PR thread: %w", err)
	}

	// Cache the new thread with its message text
	c.threadCache.Set(cacheKey, ThreadInfo{
		ThreadTS:    newThreadTS,
		ChannelID:   channelID,
		LastState:   prState,
		MessageText: newMessageText,
	})

	slog.Info("created and cached new PR thread",
		"pr", cacheKey,
		"thread_ts", newThreadTS,
		logFieldChannel, channelID,
		"initial_state", prState,
		"message_preview", newMessageText[:min(100, len(newMessageText))],
		"creation_successful", true,
		"note", "if you see duplicate threads, check if another instance created one during the same time window")

	return newThreadTS, true, newMessageText, nil
}

// searchForPRThread searches for an existing PR thread in a channel using channel history.
// This approach uses channels:history permission instead of search:read which isn't available to bots.
// Note: This is more expensive than search API but works reliably with basic bot permissions.
// Results are cached by the calling code to minimize API calls.
// Returns (threadTS, currentMessageText) - both empty if not found.
func (c *Coordinator) searchForPRThread(ctx context.Context, channelID, prURL string, prCreatedAt time.Time) (string, string) {
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
			slog.Info("received check event for PR, refreshing state",
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

	slog.Info("PR event with pre-fetched data",
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
	c.processChannelsInParallel(ctx, prCtx, channels, workspaceID)

	// Handle user notifications - send DMs to blocked users
	// This happens AFTER channel processing completes to ensure tags are tracked first
	if len(blockedOn) > 0 {
		// Deduplicate blocked users (same user might be blocked for multiple reasons)
		uniqueUsers := make(map[string]bool)
		for _, githubUser := range blockedOn {
			uniqueUsers[githubUser] = true
		}

		slog.Info("preparing to send DM notifications",
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"total_blocked_users", len(blockedOn),
			"unique_users", len(uniqueUsers),
			"will_send_async", true,
			"warning", "DMs are sent async - if instance crashes before completion, another instance may retry and send duplicates")

		// Send DMs asynchronously to avoid blocking event processing
		// Use a detached context with timeout to allow graceful completion even if parent context is cancelled
		// Note: No panic recovery - we want panics to propagate and restart the service (Cloud Run will handle it)
		// A quiet failure is worse than a visible crash that triggers automatic recovery
		dmCtx, dmCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		go func() {
			defer dmCancel()
			c.sendDMNotifications(dmCtx, workspaceID, owner, repo, prNumber, uniqueUsers, event, prState)
		}()
	} else {
		slog.Info("no users blocking PR - no notifications needed",
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"pr_state", prState)
	}
}

// sendDMNotifications sends DM notifications to unique blocked users asynchronously.
// This runs in a separate goroutine to avoid blocking event processing.
func (c *Coordinator) sendDMNotifications(
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
	prState string,
) {
	slog.Info("starting DM notification batch",
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"workspace", workspaceID,
		"user_count", len(uniqueUsers),
		"pr_state", prState)

	domain := c.configManager.Domain(owner)
	sentCount := 0
	failedCount := 0

	for githubUser := range uniqueUsers {
		// Map GitHub username to Slack user ID
		slackUserID, err := c.userMapper.SlackHandle(ctx, githubUser, owner, domain)
		if err != nil || slackUserID == "" {
			slog.Debug("could not map GitHub user to Slack - skipping DM",
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"github_user", githubUser,
				"error", err)
			continue
		}

		// Get tag info to determine which channel the user was tagged in
		tagInfo := c.notifier.Tracker.LastUserPRChannelTag(workspaceID, slackUserID, owner, repo, prNumber)

		// For channel name lookup (needed for config), we need to resolve the channel ID back to name
		// This is optional - if we can't resolve it, NotifyUser will use defaults
		var channelName string
		if tagInfo.ChannelID != "" {
			// We don't have a reverse lookup, so just pass empty string
			// NotifyUser will use default delay if channelName is empty
			channelName = ""
		}

		// Send notification using smart delay logic
		prInfo := notify.PRInfo{
			Owner:   owner,
			Repo:    repo,
			Number:  prNumber,
			Title:   event.PullRequest.Title,
			Author:  event.PullRequest.User.Login,
			State:   prState,
			HTMLURL: event.PullRequest.HTMLURL,
		}

		err = c.notifier.NotifyUser(ctx, workspaceID, slackUserID, tagInfo.ChannelID, channelName, prInfo)
		if err != nil {
			slog.Warn("failed to notify user",
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"github_user", githubUser,
				"slack_user", slackUserID,
				"error", err)
			failedCount++
		} else {
			sentCount++
		}
	}

	slog.Info("completed DM notification batch",
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"workspace", workspaceID,
		"sent_count", sentCount,
		"failed_count", failedCount,
		"total_users", len(uniqueUsers))
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

// processChannelsInParallel processes multiple channels concurrently for better performance.
func (c *Coordinator) processChannelsInParallel(
	ctx context.Context, prCtx prContext, channels []string, workspaceID string,
) {
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
		return
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
		if c.slack.IsBotInChannel(ctx, channelID) {
			validChannels = append(validChannels, channelName)
		} else {
			slog.Warn("skipping channel - bot not a member",
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
		return
	}

	slog.Debug("filtered channels for processing",
		logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
		"total_channels", len(channels),
		"valid_channels", len(validChannels),
		"filtered_out", len(channels)-len(validChannels))

	// Process channels in parallel for better performance
	// Use WaitGroup instead of errgroup since we don't want one failure to cancel others
	// Note: No panic recovery - we want panics to propagate and restart the service (Cloud Run will handle it)
	// A quiet failure is worse than a visible crash that triggers automatic recovery
	var wg sync.WaitGroup
	wg.Add(len(validChannels))

	for _, channelName := range validChannels {
		go func(ch string) {
			defer wg.Done()
			c.processPRForChannel(ctx, prCtx, ch, workspaceID)
		}(channelName)
	}

	// Wait for all channels to complete
	wg.Wait()
	slog.Debug("completed parallel channel processing",
		logFieldPR, fmt.Sprintf(prFormatString, prCtx.Owner, prCtx.Repo, prCtx.Number),
		"channels_processed", len(validChannels))
}

// processPRForChannel handles PR processing for a single channel (extracted from the main loop).
func (c *Coordinator) processPRForChannel(
	ctx context.Context, prCtx prContext, channelName, workspaceID string,
) {
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
		return
	}

	// Resolve channel name to ID for API calls
	channelID := c.slack.ResolveChannelID(ctx, channelName)

	// For display purposes, show both name and ID
	var channelDisplay string
	switch {
	case channelID != channelName:
		channelDisplay = fmt.Sprintf("#%s (%s)", channelName, channelID)
	case channelName != "" && channelName[0] == 'C':
		channelDisplay = channelID
	default:
		channelDisplay = fmt.Sprintf("#%s (unresolved)", channelName)
	}

	slog.Debug("processing PR for individual channel",
		"workspace", c.workspaceName,
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"channel", channelDisplay,
		"channel_id", channelID,
		"pr_state", prState,
		"action", event.Action)

	oldState := ""

	// Check cache for existing thread info to get old state
	prKey := fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)
	if threadInfo, exists := c.threadCache.Get(prKey); exists && threadInfo.ChannelID == channelID {
		oldState = threadInfo.LastState
	}

	// Find or create thread for this PR in this channel
	// Convert to the expected struct format
	pullRequestStruct := struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL   string    `json:"html_url"`
		Title     string    `json:"title"`
		Number    int       `json:"number"`
		CreatedAt time.Time `json:"created_at"`
	}{
		User:      event.PullRequest.User,
		HTMLURL:   event.PullRequest.HTMLURL,
		Title:     event.PullRequest.Title,
		Number:    event.PullRequest.Number,
		CreatedAt: event.PullRequest.CreatedAt,
	}
	threadTS, wasNewlyCreated, currentText, err := c.findOrCreatePRThread(ctx, channelID, owner, repo, prNumber, prState, pullRequestStruct, checkResult)
	if err != nil {
		slog.Error("failed to find or create PR thread",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"channel", channelDisplay,
			"channel_id", channelID,
			"error", err,
			"will_continue_with_next_channel", true)
		return
	}

	// Track that we notified users in this channel for DM delay logic
	c.notifier.Tracker.UpdateChannelNotification(workspaceID, owner, repo, prNumber)

	// Track user tags in channel asynchronously to avoid blocking thread creation
	// This is the critical performance optimization - email lookups take 13-20 seconds each
	// Extract GitHub usernames who are blocked on this PR
	blockedUsers := c.extractBlockedUsersFromTurnclient(checkResult)
	domain := c.configManager.Domain(owner)
	if len(blockedUsers) > 0 {
		// Run email lookups in background to avoid blocking message delivery
		// Use detached context to allow completion even if parent context is cancelled
		lookupCtx, lookupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		go func() {
			defer lookupCancel()
			for _, githubUser := range blockedUsers {
				// Map GitHub username to Slack user ID
				slackUserID, err := c.userMapper.SlackHandle(lookupCtx, githubUser, owner, domain)
				if err == nil && slackUserID != "" {
					// Track with channelID - this will only update on FIRST call per user/PR
					c.notifier.Tracker.UpdateUserPRChannelTag(workspaceID, slackUserID, channelID, owner, repo, prNumber)
					slog.Debug("tracked user tag in channel (async)",
						"workspace", workspaceID,
						"github_user", githubUser,
						"slack_user", slackUserID,
						"channel", channelDisplay,
						"channel_id", channelID,
						"pr", fmt.Sprintf(prFormatString, owner, repo, prNumber))
				}
			}
		}()
	}

	// Build what the message SHOULD be based on current PR state
	// Then compare to what it IS - update if different
	if !wasNewlyCreated {
		expectedPrefix := notify.PrefixForState(prState)
		domain := c.configManager.Domain(owner)
		urlWithState := event.PullRequest.HTMLURL + c.getStateQueryParam(prState)

		expectedText := fmt.Sprintf("%s %s <%s|%s#%d> · %s",
			expectedPrefix,
			event.PullRequest.Title,
			urlWithState,
			repo,
			prNumber,
			event.PullRequest.User.Login,
		)

		nextActions := c.formatNextActions(ctx, checkResult, owner, domain)
		if nextActions != "" {
			expectedText += fmt.Sprintf(" → %s", nextActions)
		}

		// Compare expected vs actual - update if different
		if currentText != expectedText {
			slog.Info("updating message - content changed",
				"workspace", c.workspaceName,
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"channel", channelDisplay,
				"channel_id", channelID,
				"thread_ts", threadTS,
				"pr_state", prState,
				"old_state", oldState,
				"current_message_preview", currentText[:min(100, len(currentText))],
				"expected_message_preview", expectedText[:min(100, len(expectedText))])

			if err := c.slack.UpdateMessage(ctx, channelID, threadTS, expectedText); err != nil {
				slog.Error("failed to update PR message",
					"workspace", c.workspaceName,
					logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
					"channel", channelDisplay,
					"channel_id", channelID,
					"thread_ts", threadTS,
					"error", err)
			} else {
				// Update cache with new message text
				cacheKey := fmt.Sprintf("%s/%s#%d:%s", owner, repo, prNumber, channelID)
				c.threadCache.Set(cacheKey, ThreadInfo{
					ThreadTS:    threadTS,
					ChannelID:   channelID,
					LastState:   prState,
					MessageText: expectedText,
				})
				slog.Info("successfully updated PR message",
					"workspace", c.workspaceName,
					logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
					"channel", channelDisplay,
					"channel_id", channelID,
					"thread_ts", threadTS,
					"pr_state", prState)
			}
		} else {
			slog.Debug("message already matches expected content, no update needed",
				"workspace", c.workspaceName,
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"channel", channelDisplay,
				"thread_ts", threadTS,
				"pr_state", prState)
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
		"had_state_change", oldState != "" && oldState != prState)
}

// handlePullRequestFromSprinkler handles pull request events from sprinkler by fetching PR data from GitHub API.
func (c *Coordinator) handlePullRequestFromSprinkler(
	ctx context.Context, owner, repo string, prNumber int, sprinklerURL string, eventTimestamp time.Time,
) {
	slog.Info("handling PR event from sprinkler using turnclient",
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

	// Create and authenticate turnclient
	turnClient, err := turn.NewDefaultClient()
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
		"pr_size", checkResult.Analysis.Size,
		"unresolved_comments", checkResult.Analysis.UnresolvedComments,
		"checks_state", fmt.Sprintf("%+v", checkResult.Analysis.Checks),
		"last_activity", checkResult.Analysis.LastActivity)

	// Use PR details from turnclient instead of making additional GitHub API call
	pr := checkResult.PullRequest

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
func (*Coordinator) handlePullRequestReviewFromSprinkler(ctx context.Context, owner, repo string, prNumber int, sprinklerURL string, _ time.Time) {
	slog.Info("handling PR review event from sprinkler",
		logFieldOwner, owner,
		logFieldRepo, repo,
		"pr_number", prNumber,
		"sprinkler_url", sprinklerURL,
		"note", "review events not fully implemented yet")
	// TODO: Implement review event handling if needed
}

// createPRThread creates a new thread in Slack for a PR.
// Critical performance optimization: Posts thread immediately WITHOUT user mentions,
// then updates asynchronously once email lookups complete (which take 13-20 seconds each).
func (c *Coordinator) createPRThread(ctx context.Context, channel, owner, repo string, number int, prState string, pr struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL   string    `json:"html_url"`
	Title     string    `json:"title"`
	Number    int       `json:"number"`
	CreatedAt time.Time `json:"created_at"`
}, checkResult *turn.CheckResponse,
) (threadTS string, messageText string, err error) {
	// Get state-based prefix
	prefix := notify.PrefixForState(prState)

	// Add state query param to URL for debugging
	urlWithState := pr.HTMLURL + c.getStateQueryParam(prState)

	// Format initial message WITHOUT user mentions (fast path)
	// Format: :emoji: Title repo#123 · author
	initialText := fmt.Sprintf("%s %s <%s|%s#%d> · %s",
		prefix,
		pr.Title,
		urlWithState,
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
	domain := c.configManager.Domain(owner)
	if checkResult != nil && len(checkResult.Analysis.NextAction) > 0 {
		// Use detached context to allow completion even if parent context is cancelled
		enrichCtx, enrichCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		// Capture variables to avoid data race
		capturedThreadTS := threadTS
		capturedOwner := owner
		capturedRepo := repo
		capturedNumber := number
		capturedChannel := resolvedChannel
		capturedInitialText := initialText
		go func() {
			defer enrichCancel()

			// Perform email lookups in background
			nextActions := c.formatNextActions(enrichCtx, checkResult, capturedOwner, domain)
			if nextActions != "" {
				// Update message with user mentions
				enrichedText := capturedInitialText + fmt.Sprintf(" → %s", nextActions)
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
						"next_actions", nextActions)
				}
			}
		}()
	}

	return threadTS, initialText, nil
}
