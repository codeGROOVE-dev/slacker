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
	prThreads map[string]ThreadInfo // "owner/repo#123" -> thread info
	mu        sync.RWMutex
}

// ThreadInfo stores thread information for a PR.
type ThreadInfo struct {
	UpdatedAt time.Time `json:"updated_at"`
	ThreadTS  string    `json:"thread_ts"`
	ChannelID string    `json:"channel_id"`
	LastState string    `json:"last_state"`
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
	slack         *slackpkg.Client
	github        *github.Client
	configManager *config.Manager
	notifier      *notify.Manager
	userMapper    *usermapping.Service
	sprinklerURL  string
	threadCache   *ThreadCache
	workspaceName string // Track workspace name for better logging
}

// New creates a new bot coordinator for a single GitHub organization.
func New(
	ctx context.Context,
	slackClient *slackpkg.Client,
	githubClient *github.Client,
	configManager *config.Manager,
	notifier *notify.Manager,
	sprinklerURL string,
) *Coordinator {
	c := &Coordinator{
		slack:         slackClient,
		github:        githubClient,
		configManager: configManager,
		notifier:      notifier,
		userMapper:    usermapping.New(slackClient.API(), githubClient.InstallationToken(ctx)),
		sprinklerURL:  sprinklerURL,
		threadCache: &ThreadCache{
			prThreads: make(map[string]ThreadInfo),
		},
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
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Number  int    `json:"number"`
}, checkResult *turn.CheckResponse,
) (threadTS string, wasNewlyCreated bool, err error) {
	prKey := fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)

	slog.Debug("finding or creating PR thread",
		"pr", prKey,
		logFieldChannel, channelID,
		"pr_state", prState)

	// Check cache first
	if threadInfo, exists := c.threadCache.Get(prKey); exists && threadInfo.ChannelID == channelID {
		slog.Debug("found PR thread in cache",
			"pr", prKey,
			"thread_ts", threadInfo.ThreadTS,
			logFieldChannel, channelID,
			"cached_state", threadInfo.LastState)
		return threadInfo.ThreadTS, false, nil
	}

	// Search Slack for existing thread by this bot
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
	// Use a reasonable search window - last 30 days or creation time if available
	// TODO: Once we have PR creation date in the struct, use that instead
	searchFrom := time.Now().AddDate(0, 0, -30) // 30 days fallback
	threadTS = c.searchForPRThread(ctx, channelID, prURL, searchFrom)
	if threadTS != "" {
		slog.Info("found existing PR thread via search",
			"pr", prKey,
			"thread_ts", threadTS,
			logFieldChannel, channelID)

		// Cache the found thread
		c.threadCache.Set(prKey, ThreadInfo{
			ThreadTS:  threadTS,
			ChannelID: channelID,
			LastState: prState,
		})
		return threadTS, false, nil
	}

	// Create new thread
	slog.Info("creating new PR thread",
		"pr", prKey,
		logFieldChannel, channelID,
		"pr_state", prState)

	newThreadTS, err := c.createPRThread(ctx, channelID, owner, repo, prNumber, prState, pullRequest, checkResult)
	if err != nil {
		return "", false, fmt.Errorf("failed to create PR thread: %w", err)
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
		logFieldChannel, channelID,
		"initial_state", prState)

	return newThreadTS, true, nil
}

// searchForPRThread searches for an existing PR thread in a channel using channel history.
// This approach uses channels:history permission instead of search:read which isn't available to bots.
// Note: This is more expensive than search API but works reliably with basic bot permissions.
// Results are cached by the calling code to minimize API calls.
func (c *Coordinator) searchForPRThread(ctx context.Context, channelID, prURL string, prCreatedAt time.Time) string {
	slog.Debug("searching for existing PR thread using channel history",
		logFieldChannel, channelID,
		"pr_url", prURL)

	// Get bot info to identify our messages
	botInfo, err := c.slack.BotInfo(ctx)
	if err != nil {
		slog.Warn("failed to get bot info, cannot search for existing threads",
			logFieldChannel, channelID,
			"error", err)
		// Return empty string to indicate no thread found
		return ""
	}

	// Search from PR creation date (more efficient than arbitrary 10 days)
	// Slack timestamps are in seconds since epoch
	prCreatedTimestamp := prCreatedAt.Unix()
	oldestTimestamp := strconv.FormatInt(prCreatedTimestamp, 10)

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
		// Return empty string to indicate no thread found
		// This allows graceful fallback to creating new threads
		return ""
	}

	slog.Debug("retrieved messages from channel history",
		logFieldChannel, channelID,
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
				logFieldChannel, channelID,
				"thread_ts", msg.Timestamp,
				"pr_url", prURL,
				"message_age_hours", messageAgeHours,
				"message_preview", msg.Text[:min(100, len(msg.Text))])
			return msg.Timestamp
		}
	}

	slog.Debug("no existing PR thread found in channel history",
		logFieldChannel, channelID,
		"pr_url", prURL,
		"messages_searched", len(history.Messages),
		"bot_user_id", botInfo.UserID)

	return ""
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
		// Parse to get PR number.
		// This is simplified - in production, we'd need to map commits to PRs.
		slog.Debug("received check event", "owner", owner, "repo", repo)
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
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		User    struct {
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

	slog.Info("evaluating PR for channel notifications",
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

	slog.Info("retrieved PR state for notification processing",
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
			"will_send_async", true)

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
			HTMLURL string `json:"html_url"`
			Title   string `json:"title"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
			Number int `json:"number"`
		} `json:"pull_request"`
		Number int `json:"number"`
	},
	prState string,
) {
	domain := c.configManager.Domain(owner)

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
		}
	}

	slog.Info("completed DM notification processing",
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
		"users_processed", len(uniqueUsers))
}

// extractStateFromTurnclient extracts PR state from turnclient response without additional API calls.
func (*Coordinator) extractStateFromTurnclient(checkResult *turn.CheckResponse) string {
	// Use turnclient's state analysis instead of making GitHub API calls
	// This maps turnclient states to our emoji reactions

	pr := checkResult.PullRequest
	analysis := checkResult.Analysis

	slog.Info("extracting state from turnclient data",
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

	// Group users by action kind
	actionGroups := make(map[string][]string)
	for user, action := range checkResult.Analysis.NextAction {
		actionKind := string(action.Kind)
		actionGroups[actionKind] = append(actionGroups[actionKind], user)
	}

	// Format each action group
	var parts []string
	for actionKind, users := range actionGroups {
		// Convert snake_case to space-separated words
		actionName := strings.ReplaceAll(actionKind, "_", " ")

		// Format user mentions
		userMentions := c.userMapper.FormatUserMentions(ctx, users, owner, domain)

		parts = append(parts, fmt.Sprintf("%s: %s", actionName, userMentions))
	}

	// Use semicolons to separate different actions (commas are used between users)
	return strings.Join(parts, "; ")
}

// getPrefixForState returns the emoji prefix for a given PR state.
func (*Coordinator) getPrefixForState(prState string) string {
	switch prState {
	case "tests_running":
		return ":test_tube:" // Tests running/pending
	case "tests_broken":
		return ":cockroach:" // Tests broken - needs fixing
	case "awaiting_review":
		return ":hourglass:" // Waiting on review
	case "changes_requested":
		return ":carpentry_saw:" // Approved but needs work
	case "approved":
		return ":white_check_mark:" // Reviewed & approved
	case "merged":
		return ":rocket:" // Merged
	case "closed":
		return ":man_facepalming:" // Closed but not merged
	default:
		return ":postal_horn:" // Default fallback
	}
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
			HTMLURL string `json:"html_url"`
			Title   string `json:"title"`
			User    struct {
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

	slog.Info("processing PR for all configured channels",
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

	slog.Info("filtered channels for processing",
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
			HTMLURL string `json:"html_url"`
			Title   string `json:"title"`
			User    struct {
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

	slog.Info("processing PR for individual channel",
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
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		User:    event.PullRequest.User,
		HTMLURL: event.PullRequest.HTMLURL,
		Title:   event.PullRequest.Title,
		Number:  event.PullRequest.Number,
	}
	threadTS, wasNewlyCreated, err := c.findOrCreatePRThread(ctx, channelID, owner, repo, prNumber, prState, pullRequestStruct, checkResult)
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

	// Track user tags in channel for delayed DM logic
	// Extract GitHub usernames who are blocked on this PR
	blockedUsers := c.extractBlockedUsersFromTurnclient(checkResult)
	domain := c.configManager.Domain(owner)
	for _, githubUser := range blockedUsers {
		// Map GitHub username to Slack user ID
		slackUserID, err := c.userMapper.SlackHandle(ctx, githubUser, owner, domain)
		if err == nil && slackUserID != "" {
			// Track with channelID - this will only update on FIRST call per user/PR
			c.notifier.Tracker.UpdateUserPRChannelTag(workspaceID, slackUserID, channelID, owner, repo, prNumber)
			slog.Debug("tracked user tag in channel",
				"workspace", workspaceID,
				"github_user", githubUser,
				"slack_user", slackUserID,
				"channel", channelDisplay,
				"channel_id", channelID,
				"pr", fmt.Sprintf(prFormatString, owner, repo, prNumber))
		}
	}

	// Update message prefix if state changed
	if !wasNewlyCreated && oldState != "" && oldState != prState {
		slog.Debug("updating message prefix for state change",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"channel", channelDisplay,
			"channel_id", channelID,
			"thread_ts", threadTS,
			"old_state", oldState,
			"new_state", prState)

		// Rebuild the message text with new prefix
		newPrefix := c.getPrefixForState(prState)
		domain := c.configManager.Domain(owner)
		urlWithState := event.PullRequest.HTMLURL + c.getStateQueryParam(prState)

		newText := fmt.Sprintf("%s %s <%s|%s#%d> · %s",
			newPrefix,
			event.PullRequest.Title,
			urlWithState,
			repo,
			prNumber,
			event.PullRequest.User.Login,
		)

		nextActions := c.formatNextActions(ctx, checkResult, owner, domain)
		if nextActions != "" {
			newText += fmt.Sprintf(" → %s", nextActions)
		}

		if err := c.slack.UpdateMessage(ctx, channelID, threadTS, newText); err != nil {
			slog.Error("failed to update message prefix for PR state",
				"workspace", c.workspaceName,
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"channel", channelDisplay,
				"channel_id", channelID,
				"thread_ts", threadTS,
				"old_state", oldState,
				"new_state", prState,
				"error", err)
		} else {
			slog.Debug("updated PR message prefix successfully",
				"workspace", c.workspaceName,
				logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
				"channel", channelDisplay,
				"channel_id", channelID,
				"thread_ts", threadTS,
				"old_state", oldState,
				"new_state", prState)
		}
	} else if !wasNewlyCreated {
		slog.Debug("PR state unchanged, skipping message update",
			"workspace", c.workspaceName,
			logFieldPR, fmt.Sprintf(prFormatString, owner, repo, prNumber),
			"channel", channelDisplay,
			"thread_ts", threadTS,
			"current_state", prState)
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
			HTMLURL string `json:"html_url"`
			Title   string `json:"title"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
			Number int `json:"number"`
		} `json:"pull_request"`
		Number int `json:"number"`
	}{
		Action: "synchronize", // Use synchronize as default for sprinkler notifications
		PullRequest: struct {
			HTMLURL string `json:"html_url"`
			Title   string `json:"title"`
			User    struct {
				Login string `json:"login"`
			} `json:"user"`
			Number int `json:"number"`
		}{
			HTMLURL: prURL,
			Title:   pr.Title,
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
func (c *Coordinator) createPRThread(ctx context.Context, channel, owner, repo string, number int, prState string, pr struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
	Number  int    `json:"number"`
}, checkResult *turn.CheckResponse,
) (string, error) {
	// Get state-based prefix and domain for user mapping
	prefix := c.getPrefixForState(prState)
	domain := c.configManager.Domain(owner)

	// Add state query param to URL for debugging
	urlWithState := pr.HTMLURL + c.getStateQueryParam(prState)

	// Format message: :emoji: Title repo#123 · author → action (user1, user2)
	text := fmt.Sprintf("%s %s <%s|%s#%d> · %s",
		prefix,
		pr.Title,
		urlWithState,
		repo,
		number,
		pr.User.Login,
	)

	// Add next actions if we have any
	nextActions := c.formatNextActions(ctx, checkResult, owner, domain)
	if nextActions != "" {
		text += fmt.Sprintf(" → %s", nextActions)
	}

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

	// Add initial reaction based on state from turnclient if available
	// For createPRThread, we may not have turnclient data available, so this is optional
	// The reaction will be set properly when the PR is processed through the main flow
	slog.Debug("thread created, reaction will be set by main processing flow",
		logFieldPR, fmt.Sprintf(prFormatString, owner, repo, number),
		logFieldChannel, resolvedChannel,
		"thread_ts", threadTS)

	return threadTS, nil
}
