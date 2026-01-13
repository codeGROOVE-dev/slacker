// Package slack provides a Slack API client and interaction handlers.
package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// Errors.
var (
	// ErrNoDMToUpdate indicates no DM exists to update.
	ErrNoDMToUpdate = errors.New("no DM found to update")
)

// DMLocation represents a DM message location in Slack.
type DMLocation struct {
	ChannelID string
	MessageTS string
}

// Constants for input validation.
const (
	maxCommandInputLength = 200
	logFieldChannelID     = "channel_id"
)

// cacheEntry represents a cached value with expiration.
type cacheEntry struct {
	value     any
	expiresAt time.Time
}

// apiCache provides thread-safe caching for Slack API responses.
type apiCache struct {
	entries map[string]cacheEntry
	mu      sync.RWMutex
	hits    int64 // Cache hit counter
	misses  int64 // Cache miss counter
}

// Client wraps the Slack API client with caching.
//
//nolint:govet // Field order optimized for logical grouping over memory alignment
type Client struct {
	homeViewHandlerMu sync.RWMutex
	reportHandlerMu   sync.RWMutex
	stateStoreMu      sync.RWMutex
	signingSecret     string
	teamID            string     // Workspace team ID
	stateStore        StateStore // State store for DM message tracking
	api               API        // Slack API interface for testability
	cache             *apiCache
	manager           *Manager                                               // Reference to manager for cache invalidation
	homeViewHandler   func(ctx context.Context, teamID, userID string) error // Callback for app_home_opened events
	reportHandler     func(ctx context.Context, teamID, userID string) error // Callback for /goose report slash command
	retryDelay        time.Duration                                          // Base delay for retries (default: 2s, can be overridden for tests)
}

// set stores a value in the cache with TTL.
func (c *apiCache) set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
}

// get retrieves a value from the cache if not expired.
// Expired entries are automatically removed.
func (c *apiCache) get(key string) (any, bool) {
	c.mu.Lock() // Use Lock instead of RLock to allow deletion
	defer c.mu.Unlock()
	entry, exists := c.entries[key]
	if !exists {
		c.misses++
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key) // Clean up expired entry
		c.misses++
		return nil, false
	}
	c.hits++
	return entry.value, true
}

// invalidate removes a specific cache entry (useful for setup scenarios).
func (c *apiCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// InvalidateChannel removes cached data for a specific channel (helpful during setup).
func (c *Client) InvalidateChannel(channelID string) {
	c.invalidateChannelCache(channelID)
}

// invalidateChannelCache removes cached data for a specific channel.
func (c *Client) invalidateChannelCache(channelID string) {
	// Clear channel membership cache
	membershipKey := fmt.Sprintf("bot_in_channel_%s", channelID)
	c.cache.invalidate(membershipKey)

	// Clear channel resolution cache (in case channel name->ID mapping changed)
	// Note: We can't easily reverse lookup channel names from ID, but this is less critical
	// since channel resolution is primarily name->ID direction

	slog.Debug("invalidated channel caches", "channel_id", channelID, "cleared", "membership")
}

// getRetryDelay returns the retry delay to use, defaulting to 2 seconds if not set.
func (c *Client) getRetryDelay() time.Duration {
	if c.retryDelay == 0 {
		return 2 * time.Second
	}
	return c.retryDelay
}

// New creates a new Slack client with caching.
func New(token, signingSecret string) *Client {
	return &Client{
		api:           newAPIWrapper(slack.New(token)),
		signingSecret: signingSecret,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
}

// SetHomeViewHandler registers a callback for app_home_opened events.
func (c *Client) SetHomeViewHandler(handler func(ctx context.Context, teamID, userID string) error) {
	c.homeViewHandlerMu.Lock()
	defer c.homeViewHandlerMu.Unlock()
	c.homeViewHandler = handler
}

// SetReportHandler registers a callback for /goose report slash command.
func (c *Client) SetReportHandler(handler func(ctx context.Context, teamID, userID string) error) {
	c.reportHandlerMu.Lock()
	defer c.reportHandlerMu.Unlock()
	c.reportHandler = handler
}

// SetTeamID sets the team ID for this client.
func (c *Client) SetTeamID(teamID string) {
	c.teamID = teamID
}

// SetStateStore sets the state store for DM message tracking.
func (c *Client) SetStateStore(store StateStore) {
	c.stateStoreMu.Lock()
	defer c.stateStoreMu.Unlock()
	c.stateStore = store
}

// SetManager sets the manager reference for cache invalidation.
func (c *Client) SetManager(manager *Manager) {
	c.manager = manager
}

// WorkspaceInfo returns information about the current workspace (cached for 1 hour).
func (c *Client) WorkspaceInfo(ctx context.Context) (*slack.TeamInfo, error) {
	cacheKey := "team_info"

	// Try cache first
	if cached, found := c.cache.get(cacheKey); found {
		if teamInfo, ok := cached.(*slack.TeamInfo); ok {
			slog.Debug("using cached team info")
			return teamInfo, nil
		}
		slog.Warn("cached team info has incorrect type, refreshing")
		c.cache.invalidate(cacheKey)
	}

	// Fetch from API
	slog.Debug("fetching team info from Slack API")
	var teamInfo *slack.TeamInfo
	err := retry.Do(
		func() error {
			var err error
			teamInfo, err = c.api.GetTeamInfoContext(ctx)
			if err != nil {
				if isRateLimitError(err) {
					return err // Retry
				}
				return retry.Unrecoverable(err) // Don't retry auth errors etc.
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get team info: %w", err)
	}

	// Cache for 1 hour (team info rarely changes)
	c.cache.set(cacheKey, teamInfo, time.Hour)
	slog.Debug("cached team info", "team_id", teamInfo.ID, "team_domain", teamInfo.Domain)

	return teamInfo, nil
}

// PostThread creates a new thread in a channel for a PR with retry logic.
func (c *Client) PostThread(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
	slog.Info("posting thread to channel",
		"channel_id", channelID,
		"text_preview", func() string {
			if len(text) > 100 {
				return text[:100] + "..."
			}
			return text
		}(),
		"attachments_count", len(attachments))

	// Check if bot is in the channel before attempting to post
	if !c.IsBotInChannel(ctx, channelID) {
		// Try to determine if channel exists by attempting to get channel info
		_, err := c.api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{
			ChannelID: channelID,
		})
		if err != nil && strings.Contains(err.Error(), "channel_not_found") {
			return "", fmt.Errorf("channel %s does not exist - please create the channel first", channelID)
		}
		return "", fmt.Errorf("bot is not a member of channel %s - please invite the bot to the channel first", channelID)
	}

	// Disable unfurling for GitHub links.
	options := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionAttachments(attachments...),
		slack.MsgOptionDisableLinkUnfurl(),
	}

	var timestamp string
	err := retry.Do(
		func() error {
			var err error
			_, timestamp, err = c.api.PostMessageContext(ctx, channelID, options...)
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited posting, backing off", "channel", channelID)
					return err
				}
				// Check if channel not found
				if strings.Contains(err.Error(), "channel_not_found") ||
					strings.Contains(err.Error(), "not_in_channel") {
					slog.Warn("channel not found, not retrying", "channel", channelID)
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to post message, retrying", "channel", channelID, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return "", fmt.Errorf("failed to post message after retries: %w", err)
	}

	slog.Info("successfully posted thread",
		"thread_timestamp", timestamp,
		"channel_id", channelID,
		"channel_id_format", func() string {
			if channelID != "" && channelID[0] == 'C' {
				return "slack_channel_id"
			} else if channelID != "" && channelID[0] == '#' {
				return "channel_name_with_hash"
			}
			return "channel_name_without_hash"
		}())
	return timestamp, nil
}

// UpdateMessage updates an existing message with retry logic.
func (c *Client) UpdateMessage(ctx context.Context, channelID, timestamp, text string) error {
	slog.Debug("updating message",
		"channel_id", channelID,
		"timestamp", timestamp,
		"text_preview", func() string {
			if len(text) > 100 {
				return text[:100] + "..."
			}
			return text
		}())

	// Disable unfurling for GitHub links.
	options := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionDisableLinkUnfurl(),
	}

	delay := c.getRetryDelay()

	err := retry.Do(
		func() error {
			_, _, _, err := c.api.UpdateMessageContext(ctx, channelID, timestamp, options...)
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited updating message, backing off", "channel", channelID, "timestamp", timestamp)
					return err
				}
				// Don't retry on permanent errors
				if strings.Contains(err.Error(), "message_not_found") ||
					strings.Contains(err.Error(), "channel_not_found") ||
					strings.Contains(err.Error(), "not_in_channel") {
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to update message, retrying", "channel", channelID, "timestamp", timestamp, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(delay),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(delay/2),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to update message after retries: %w", err)
	}

	slog.Debug("successfully updated message",
		"timestamp", timestamp,
		"channel_id", channelID)
	return nil
}

// PostThreadReply posts a reply to an existing thread with retry logic.
func (c *Client) PostThreadReply(ctx context.Context, channelID, threadTS, text string) error {
	options := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(threadTS),
	}

	err := retry.Do(
		func() error {
			_, _, err := c.api.PostMessageContext(ctx, channelID, options...)
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited posting reply, backing off", "channel", channelID, "thread", threadTS)
					return err
				}
				// Don't retry on permanent errors
				if strings.Contains(err.Error(), "channel_not_found") ||
					strings.Contains(err.Error(), "not_in_channel") ||
					strings.Contains(err.Error(), "thread_not_found") {
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to post reply, retrying", "channel", channelID, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to post reply after retries: %w", err)
	}

	return nil
}

// HasRecentDMAboutPR checks if we recently sent a DM to this user about this PR (within last hour).
// This prevents duplicate DMs during rolling deployments.
func (c *Client) HasRecentDMAboutPR(ctx context.Context, userID, prURL string) (bool, error) {
	// Open conversation to get DM channel ID
	channel, _, _, err := c.api.OpenConversationContext(ctx, &slack.OpenConversationParameters{
		Users: []string{userID},
	})
	if err != nil {
		return false, fmt.Errorf("failed to open conversation: %w", err)
	}

	// Get bot info to identify our messages
	botInfo, err := c.BotInfo(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get bot info: %w", err)
	}

	// Search last hour of DM history
	oneHourAgo := time.Now().Add(-1 * time.Hour).Unix()
	history, err := c.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
		ChannelID: channel.ID,
		Oldest:    strconv.FormatInt(oneHourAgo, 10),
		Limit:     100, // Should be plenty for 1 hour of DMs
	})
	if err != nil {
		// If we can't check history, err on the side of sending (false negative better than false positive)
		slog.Warn("failed to get DM history for dedup check, will send DM anyway",
			"user", userID,
			"error", err)
		return false, nil
	}

	// Check if any recent messages from us contain this PR URL
	for i := range history.Messages {
		msg := &history.Messages[i]
		if msg.User == botInfo.UserID && strings.Contains(msg.Text, prURL) {
			slog.Info("found recent DM about this PR, skipping duplicate",
				"user", userID,
				"pr_url", prURL,
				"message_ts", msg.Timestamp)
			return true, nil
		}
	}

	return false, nil
}

// SendDirectMessage sends a direct message to a user with retry logic.
func (c *Client) SendDirectMessage(ctx context.Context, userID, text string) (dmChannelID, messageTS string, err error) {
	slog.Info("sending DM to user", "user", userID)

	var channelID string

	// First, open conversation with retry
	err = retry.Do(
		func() error {
			channel, _, _, err := c.api.OpenConversationContext(ctx, &slack.OpenConversationParameters{
				Users: []string{userID},
			})
			if err != nil {
				slog.Warn("failed to open conversation, retrying", "user", userID, "error", err)
				return err
			}
			channelID = channel.ID
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to open conversation after retries: %w", err)
	}

	var msgTS string
	// Then send message with retry
	// Disable unfurling for GitHub links in DMs.
	options := []slack.MsgOption{
		slack.MsgOptionText(text, false),
		slack.MsgOptionDisableLinkUnfurl(),
	}
	err = retry.Do(
		func() error {
			_, ts, err := c.api.PostMessageContext(ctx, channelID, options...)
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited sending DM, backing off", "user", userID)
					return err
				}
				slog.Warn("failed to send DM, retrying", "user", userID, "error", err)
				return err
			}
			msgTS = ts
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to send DM after retries: %w", err)
	}

	slog.Info("successfully sent DM", "user", userID, "channel_id", channelID, "message_ts", msgTS)
	return channelID, msgTS, nil
}

// SendDirectMessageWithBlocks sends a direct message to a user with Block Kit blocks.
func (c *Client) SendDirectMessageWithBlocks(ctx context.Context, userID string, blocks []slack.Block) (dmChannelID, messageTS string, err error) {
	slog.Info("sending Block Kit DM to user", "user", userID, "block_count", len(blocks))

	var channelID string

	// First, open conversation with retry
	err = retry.Do(
		func() error {
			channel, _, _, err := c.api.OpenConversationContext(ctx, &slack.OpenConversationParameters{
				Users: []string{userID},
			})
			if err != nil {
				slog.Warn("failed to open conversation, retrying", "user", userID, "error", err)
				return err
			}
			channelID = channel.ID
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to open conversation after retries: %w", err)
	}

	var msgTS string
	// Then send message with blocks with retry
	// Disable unfurling for GitHub links in DMs.
	options := []slack.MsgOption{
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionText("Daily PR Report", false), // Fallback text for notifications
		slack.MsgOptionDisableLinkUnfurl(),
	}
	err = retry.Do(
		func() error {
			_, ts, err := c.api.PostMessageContext(ctx, channelID, options...)
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited sending Block Kit DM, backing off", "user", userID)
					return err
				}
				slog.Warn("failed to send Block Kit DM, retrying", "user", userID, "error", err)
				return err
			}
			msgTS = ts
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to send Block Kit DM after retries: %w", err)
	}

	slog.Info("successfully sent Block Kit DM", "user", userID, "channel_id", channelID, "message_ts", msgTS)
	return channelID, msgTS, nil
}

// SaveDMMessageInfo saves DM message info to state store for future updates.
func (c *Client) SaveDMMessageInfo(ctx context.Context, userID, prURL, channelID, messageTS, messageText string) error {
	c.stateStoreMu.RLock()
	store := c.stateStore
	c.stateStoreMu.RUnlock()

	if store == nil {
		slog.Debug("no state store configured, skipping DM message save", "user", userID, "pr_url", prURL)
		return nil
	}

	info := state.DMInfo{
		ChannelID:   channelID,
		MessageTS:   messageTS,
		MessageText: messageText,
		SentAt:      time.Now(),
	}

	if err := store.SaveDMMessage(ctx, userID, prURL, info); err != nil {
		return fmt.Errorf("failed to save DM message info: %w", err)
	}

	slog.Debug("saved DM message info for future updates",
		"user", userID,
		"pr_url", prURL,
		"channel_id", channelID,
		"message_ts", messageTS)

	return nil
}

// UpdateDMMessage updates a previously sent DM message.
// Returns nil and logs debug if no DM exists, returns error if update fails.
func (c *Client) UpdateDMMessage(ctx context.Context, userID, prURL, newText string) error {
	c.stateStoreMu.RLock()
	store := c.stateStore
	c.stateStoreMu.RUnlock()

	if store == nil {
		slog.Debug("no state store configured, cannot update DM", "user", userID, "pr_url", prURL)
		return ErrNoDMToUpdate
	}

	// Get stored DM message info
	info, exists := store.DMMessage(ctx, userID, prURL)
	if !exists {
		slog.Debug("no DM message found to update",
			"user", userID,
			"pr_url", prURL,
			"reason", "never sent or too old")
		return ErrNoDMToUpdate
	}

	// Update the message using Slack API
	if err := c.UpdateMessage(ctx, info.ChannelID, info.MessageTS, newText); err != nil {
		return fmt.Errorf("failed to update DM message: %w", err)
	}

	// Update stored message text
	info.MessageText = newText
	if err := store.SaveDMMessage(ctx, userID, prURL, info); err != nil {
		slog.Warn("failed to update stored DM message text",
			"user", userID,
			"pr_url", prURL,
			"error", err)
	}

	slog.Info("updated DM message",
		"user", userID,
		"pr_url", prURL,
		"channel_id", info.ChannelID,
		"message_ts", info.MessageTS)

	return nil
}

// FindDMMessagesInHistory searches Slack DM history to find existing messages about a PR.
// This is the fallback when cache/datastore don't have the DM location.
// Searches since the given time in DM history using the Slack API directly.
func (c *Client) FindDMMessagesInHistory(ctx context.Context, userID, prURL string, since time.Time) ([]DMLocation, error) {
	// Open DM channel with user
	channel, _, _, err := c.api.OpenConversationContext(ctx, &slack.OpenConversationParameters{
		Users: []string{userID},
	})
	if err != nil {
		return nil, fmt.Errorf("open conversation: %w", err)
	}
	dmChannelID := channel.ID

	// Get bot info to filter our messages only
	botInfo, err := c.BotInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("get bot info: %w", err)
	}

	// Convert time to Slack timestamp format
	oldest := strconv.FormatInt(since.Unix(), 10)

	var allLocations []DMLocation
	cursor := ""

	for {
		// Fetch messages with pagination
		history, err := c.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{
			ChannelID: dmChannelID,
			Oldest:    oldest,
			Cursor:    cursor,
			Limit:     100,
		})
		if err != nil {
			return nil, fmt.Errorf("get conversation history: %w", err)
		}

		// Search for PR URL in bot's messages only
		for i := range history.Messages {
			msg := &history.Messages[i]
			if msg.User == botInfo.UserID && strings.Contains(msg.Text, prURL) {
				allLocations = append(allLocations, DMLocation{
					ChannelID: dmChannelID,
					MessageTS: msg.Timestamp,
				})
				slog.Debug("found DM in history",
					"user", userID,
					"pr", prURL,
					"message_ts", msg.Timestamp)
			}
		}

		// Check for more pages
		if !history.HasMore {
			break
		}
		cursor = history.ResponseMetaData.NextCursor
	}

	return allLocations, nil
}

// UserInfo gets user information including timezone with retry logic.
func (c *Client) UserInfo(ctx context.Context, userID string) (*slack.User, error) {
	var user *slack.User
	err := retry.Do(
		func() error {
			var err error
			user, err = c.api.GetUserInfoContext(ctx, userID)
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited getting user info, backing off", "user", userID)
					return err
				}
				// Don't retry on user_not_found
				if strings.Contains(err.Error(), "user_not_found") {
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to get user info, retrying", "user", userID, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info after retries: %w", err)
	}
	return user, nil
}

// UserPresence gets user presence (active/away) with retry logic.
func (c *Client) UserPresence(ctx context.Context, userID string) (string, error) {
	var presence *slack.UserPresence
	err := retry.Do(
		func() error {
			var err error
			presence, err = c.api.GetUserPresenceContext(ctx, userID)
			if err != nil {
				if isRateLimitError(err) {
					slog.Debug("rate limited getting presence, backing off", "user", userID)
					return err
				}
				// Don't retry on user_not_found
				if strings.Contains(err.Error(), "user_not_found") {
					return retry.Unrecoverable(err)
				}
				slog.Debug("failed to get presence, retrying", "user", userID, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get user presence after retries: %w", err)
	}
	return presence.Presence, nil
}

// IsUserActive checks if a user is currently active.
func (c *Client) IsUserActive(ctx context.Context, userID string) bool {
	presence, err := c.UserPresence(ctx, userID)
	if err != nil {
		slog.Warn("failed to get presence for user", "user", userID, "error", err)
		return false
	}
	return presence == "active"
}

// UserTimezone returns the IANA timezone string for a user (e.g., "America/New_York").
// Uses caching to minimize API calls.
func (c *Client) UserTimezone(ctx context.Context, userID string) (string, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("user_tz_%s", userID)
	if cached, exists := c.cache.get(cacheKey); exists {
		if tz, ok := cached.(string); ok {
			return tz, nil
		}
	}

	// Fetch from Slack API
	user, err := c.api.GetUserInfoContext(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get user info: %w", err)
	}

	// Extract timezone
	tz := user.TZ
	if tz == "" {
		// Fallback to UTC if no timezone set
		slog.Debug("user has no timezone set, defaulting to UTC", "user", userID)
		tz = "UTC"
	}

	// Cache for 24 hours (timezones don't change often)
	c.cache.set(cacheKey, tz, 24*time.Hour)

	slog.Debug("retrieved user timezone",
		"user", userID,
		"timezone", tz,
		"cached", true)

	return tz, nil
}

// EventsHandler handles Slack events.
func (c *Client) EventsHandler(writer http.ResponseWriter, r *http.Request) {
	// Read body for verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read body", "error", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify the signature.
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	if !c.verifyRequestSignature(signature, timestamp, body) {
		slog.Warn("failed to verify signature")
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}

	eventsAPIEvent, err := slackevents.ParseEvent(body, slackevents.OptionNoVerifyToken())
	if err != nil {
		slog.Warn("failed to parse Slack event", "error", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// Handle URL verification.
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var challenge slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &challenge); err != nil {
			slog.Error("failed to unmarshal challenge", "error", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "text/plain")
		writer.WriteHeader(http.StatusOK)
		if _, err := writer.Write([]byte(challenge.Challenge)); err != nil {
			slog.Error("failed to write challenge response", "error", err)
		}
		return
	}

	// Handle callback events.
	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		switch evt := eventsAPIEvent.InnerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			// Handle message events if needed.
			slog.Debug("received message event", "event", evt)
		case *slackevents.AppMentionEvent:
			// Handle app mentions if needed.
			slog.Debug("received app mention", "event", evt)
		case *slackevents.AppHomeOpenedEvent:
			// Update app home when user opens it
			slog.Info("app home opened - rendering fresh dashboard",
				"user_id", evt.User,
				"team_id", c.teamID,
				"tab", evt.Tab,
				"trigger", "slack_event")

			// Call registered home view handler if present
			c.homeViewHandlerMu.RLock()
			handler := c.homeViewHandler
			c.homeViewHandlerMu.RUnlock()

			if handler != nil {
				homeCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
				defer cancel()

				if err := handler(homeCtx, c.teamID, evt.User); err != nil {
					slog.Error("home view handler failed",
						"team_id", c.teamID,
						"user", evt.User,
						"error", err)
				} else {
					slog.Info("successfully rendered home view",
						"team_id", c.teamID,
						"user", evt.User,
						"trigger", "slack_event")
				}
			} else {
				slog.Debug("no home view handler registered", "user", evt.User)
			}
		case *slackevents.MemberJoinedChannelEvent:
			// Bot was added to a channel - invalidate cache
			slog.Info("bot joined channel - invalidating cache",
				"channel_id", evt.Channel,
				"user_id", evt.User,
				"inviter", evt.Inviter)
			c.invalidateChannelCache(evt.Channel)
		case *slackevents.MemberLeftChannelEvent:
			// Bot was removed from a channel - invalidate cache
			slog.Info("bot left channel - invalidating cache",
				"channel_id", evt.Channel,
				"user_id", evt.User)
			c.invalidateChannelCache(evt.Channel)
		case *slackevents.TokensRevokedEvent:
			// Tokens revoked - invalidate workspace client cache to force refresh
			slog.Warn("tokens revoked event received - invalidating workspace cache",
				"team_id", c.teamID)
			if c.manager != nil && c.teamID != "" {
				c.manager.InvalidateCache(c.teamID)
				slog.Info("invalidated workspace cache due to auth event",
					"team_id", c.teamID)
			}
		case *slackevents.AppUninstalledEvent:
			// App uninstalled - invalidate workspace client cache
			slog.Warn("app uninstalled event received",
				"team_id", c.teamID)
			if c.manager != nil && c.teamID != "" {
				c.manager.InvalidateCache(c.teamID)
				slog.Info("invalidated workspace cache due to auth event",
					"team_id", c.teamID)
			}
		}
	}

	writer.WriteHeader(http.StatusOK)
}

// InteractionsHandler handles Slack interactive components.
func (c *Client) InteractionsHandler(writer http.ResponseWriter, r *http.Request) {
	// Parse the payload.
	payload := r.FormValue("payload")
	if payload == "" {
		slog.Error("interaction missing payload",
			"remote_addr", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"))
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	var interaction slack.InteractionCallback
	if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
		slog.Error("failed to unmarshal interaction",
			"error", err,
			"payload_size", len(payload),
			"remote_addr", r.RemoteAddr)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// NOTE: Signature verification is handled by EventRouter before routing here.
	// We don't verify again because FormValue() already consumed the body above.

	// Log the interaction for debugging
	var actionIDs []string
	if interaction.Type == slack.InteractionTypeBlockActions {
		for _, action := range interaction.ActionCallback.BlockActions {
			actionIDs = append(actionIDs, action.ActionID)
		}
	}

	slog.Info("processing interaction",
		"type", interaction.Type,
		"team_id", interaction.Team.ID,
		"user_id", interaction.User.ID,
		"user_name", interaction.User.Name,
		"action_ids", actionIDs,
		"remote_addr", r.RemoteAddr)

	// Handle different interaction types.
	switch interaction.Type {
	case slack.InteractionTypeBlockActions:
		// Handle block actions (buttons, selects, etc.).
		c.handleBlockAction(r.Context(), &interaction)
	case slack.InteractionTypeViewSubmission:
		// Handle modal submissions.
		slog.Debug("received view submission", "interaction", interaction)
	default:
		// Other interaction types
		slog.Debug("unhandled interaction type", "type", interaction.Type)
	}

	slog.Debug("interaction handled successfully",
		"type", interaction.Type,
		"user_id", interaction.User.ID,
		"response_status", http.StatusOK)
	writer.WriteHeader(http.StatusOK)
}

// handleBlockAction handles block action interactions (button clicks, etc.).
func (c *Client) handleBlockAction(ctx context.Context, interaction *slack.InteractionCallback) {
	// Process each action in the callback
	for _, action := range interaction.ActionCallback.BlockActions {
		slog.Debug("processing block action",
			"action_id", action.ActionID,
			"user", interaction.User.ID,
			"team", interaction.Team.ID)

		switch action.ActionID {
		case "refresh_dashboard":
			c.handleRefreshDashboard(ctx, interaction)
		default:
			slog.Debug("unhandled action_id", "action_id", action.ActionID)
		}
	}
}

// handleRefreshDashboard handles the refresh dashboard button action.
// It runs the dashboard fetch asynchronously to avoid Slack's 3-second interaction timeout.
//
//nolint:unparam // ctx intentionally unused - we use context.Background() in goroutine
func (c *Client) handleRefreshDashboard(ctx context.Context, interaction *slack.InteractionCallback) {
	c.homeViewHandlerMu.RLock()
	handler := c.homeViewHandler
	c.homeViewHandlerMu.RUnlock()

	if handler == nil {
		slog.Warn("refresh requested but no home view handler registered",
			"user", interaction.User.ID)
		return
	}

	slog.Info("refreshing dashboard via button click (async)",
		"team_id", interaction.Team.ID,
		"user_id", interaction.User.ID,
		"trigger", "refresh_button")

	// Run dashboard fetch asynchronously to avoid blocking the HTTP response.
	// Slack requires interactions to be acknowledged within 3 seconds.
	// We use context.Background() because the HTTP request context will be cancelled
	// after we return, but we want the dashboard fetch to continue.
	//nolint:contextcheck // Intentionally using Background() - HTTP context cancelled after return
	go func() {
		handlerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := handler(handlerCtx, interaction.Team.ID, interaction.User.ID); err != nil {
			slog.Error("failed to refresh dashboard",
				"team_id", interaction.Team.ID,
				"user_id", interaction.User.ID,
				"trigger", "refresh_button",
				"error", err)
		} else {
			slog.Info("successfully refreshed dashboard",
				"team_id", interaction.Team.ID,
				"user_id", interaction.User.ID,
				"trigger", "refresh_button")
		}
	}()
}

// SlashCommandHandler handles Slack slash commands.
func (c *Client) SlashCommandHandler(writer http.ResponseWriter, r *http.Request) {
	// Verify the request signature.
	if !c.verifyRequest(r) {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Parse the command.
	cmd, err := slack.SlashCommandParse(r)
	if err != nil {
		slog.Error("failed to parse slash command", "error", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// Handle different commands.
	var response string
	switch cmd.Command {
	case "/goose":
		response = c.handleGooseCommand(r.Context(), &cmd)
	default:
		response = "Unknown command"
	}

	// Send response.
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(writer).Encode(map[string]string{
		"text": response,
	}); err != nil {
		slog.Error("failed to encode slash command response", "error", err)
	}
}

// handleGooseCommand handles the /goose slash command.
func (c *Client) handleGooseCommand(ctx context.Context, cmd *slack.SlashCommand) string {
	// Sanitize and validate input.
	text := strings.TrimSpace(cmd.Text)
	if len(text) > maxCommandInputLength { // Reasonable limit for command input.
		return "Command too long. Please use shorter commands."
	}

	args := strings.Fields(text)
	if len(args) == 0 {
		return "Usage: /goose [dashboard|settings|report|help]"
	}

	// Validate command argument.
	command := strings.ToLower(args[0])
	switch command {
	case "dashboard":
		// Note: In a full implementation, we'd send blocks here instead of plain text.
		// For now, return a link to the web dashboard.
		// SECURITY: URL encode user ID to prevent injection attacks
		return fmt.Sprintf("View your dashboard at: https://reviewgoose.dev/?user=%s\n"+
			"Or use the Home tab in this app for the native Slack experience.", url.QueryEscape(cmd.UserID))
	case "settings":
		return "Open the Home tab in this app to configure your notification preferences."
	case "report":
		// Call the registered report handler if available
		c.reportHandlerMu.RLock()
		handler := c.reportHandler
		c.reportHandlerMu.RUnlock()

		if handler == nil {
			slog.Warn("report requested but no report handler registered", "user", cmd.UserID)
			return "Daily report feature is not currently available. Please try again later."
		}

		// Run handler asynchronously to avoid Slack's 3-second timeout
		// The handler sends a DM directly, so we just acknowledge the request
		slog.Info("generating manual daily report via slash command",
			"team_id", c.teamID,
			"user_id", cmd.UserID,
			"trigger", "slash_command")

		go func(baseCtx context.Context) {
			// Create context that won't be cancelled when request ends
			// but inherits values from the request context
			bgCtx := context.WithoutCancel(baseCtx)
			bgCtx, cancel := context.WithTimeout(bgCtx, 30*time.Second)
			defer cancel()

			if err := handler(bgCtx, c.teamID, cmd.UserID); err != nil {
				slog.Error("failed to generate daily report",
					"team_id", c.teamID,
					"user_id", cmd.UserID,
					"trigger", "slash_command",
					"error", err)
				return
			}

			slog.Info("successfully sent manual daily report",
				"team_id", c.teamID,
				"user_id", cmd.UserID,
				"trigger", "slash_command")
		}(ctx)

		// Return immediately to avoid timeout
		return "⏳ Generating your daily report..."
	case "help":
		return "*reviewGOOSE:Slack* helps you stay on top of pull requests.\n\n" +
			"*Commands:*\n" +
			"• `/goose dashboard` - View your PR dashboard\n" +
			"• `/goose settings` - Configure notification preferences\n" +
			"• `/goose report` - Generate and send your daily PR report now\n" +
			"• `/goose help` - Show this help message\n\n" +
			"You can also visit the *Home* tab in this app for a full dashboard.\n" +
			"Learn more at https://codegroove.dev/reviewgoose/"
	default:
		return "Unknown subcommand. Try: /goose help"
	}
}

// VerifySignature verifies a Slack request signature (exported for use by EventRouter).
func (c *Client) VerifySignature(signature, timestamp string, body []byte) bool {
	return c.verifyRequestSignature(signature, timestamp, body)
}

// verifyRequestSignature verifies a Slack request signature.
func (c *Client) verifyRequestSignature(signature, timestamp string, body []byte) bool {
	// Check timestamp to prevent replay attacks (60 seconds window).
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if time.Since(time.Unix(ts, 0)) > 60*time.Second {
		return false
	}

	// Create the signature base string.
	sigBasestring := fmt.Sprintf("v0:%s:%s", timestamp, string(body))

	// Calculate expected signature.
	h := hmac.New(sha256.New, []byte(c.signingSecret))
	h.Write([]byte(sigBasestring))
	expectedSig := fmt.Sprintf("v0=%s", hex.EncodeToString(h.Sum(nil)))

	// Compare signatures.
	return hmac.Equal([]byte(expectedSig), []byte(signature))
}

// verifyRequest verifies a Slack request using headers.
func (c *Client) verifyRequest(r *http.Request) bool {
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")

	// Read body into buffer to allow re-reading.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	// Restore the body for subsequent reads.
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	return c.verifyRequestSignature(signature, timestamp, body)
}

// isRateLimitError checks if error is a rate limit error.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "rate_limited") ||
		strings.Contains(err.Error(), "429")
}

// PublishHomeView publishes a view to a user's app home with retry logic.
func (c *Client) PublishHomeView(ctx context.Context, userID string, blocks []slack.Block) error {
	view := slack.HomeTabViewRequest{
		Type:   "home",
		Blocks: slack.Blocks{BlockSet: blocks},
	}

	err := retry.Do(
		func() error {
			// Properly omit hash field to avoid hash_conflict errors
			_, err := c.api.PublishViewContext(ctx, slack.PublishViewContextRequest{
				UserID: userID,
				View:   view,
				Hash:   nil, // Properly omits hash field
			})
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited publishing home view, backing off", "user", userID)
					return err
				}
				// invalid_auth should propagate up to home handler for proper retry with fresh client
				if strings.Contains(err.Error(), "invalid_auth") {
					return retry.Unrecoverable(err)
				}
				// Don't retry on user_not_found
				if strings.Contains(err.Error(), "user_not_found") {
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to publish home view, retrying", "user", userID, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(2),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		return fmt.Errorf("failed to publish home view after retries: %w", err)
	}
	return nil
}

// SearchMessages searches for messages using the Slack API.
func (c *Client) SearchMessages(ctx context.Context, query string, params *slack.SearchParameters) (*slack.SearchMessages, error) {
	var result *slack.SearchMessages
	err := retry.Do(
		func() error {
			var err error
			result, err = c.api.SearchMessagesContext(ctx, query, *params)
			if err != nil {
				if isRateLimitError(err) {
					return err // Retry
				}
				return retry.Unrecoverable(err)
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	return result, err
}

// API returns the underlying Slack API client for compatibility.
// This unwraps the API interface to return the raw *slack.Client.
// Only use this when integrating with code that hasn't been refactored
// to use the API interface yet.
func (c *Client) API() *slack.Client {
	if wrapper, ok := c.api.(*slackAPIWrapper); ok {
		return wrapper.RawClient()
	}
	// If it's a mock or other implementation, return nil
	// Callers should handle this gracefully
	return nil
}

// ChannelHistory retrieves channel message history with optional time filtering.
func (c *Client) ChannelHistory(
	ctx context.Context, channelID string, oldest, latest string, limit int,
) (*slack.GetConversationHistoryResponse, error) {
	params := &slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Limit:     limit,
		Oldest:    oldest,
		Latest:    latest,
	}

	var result *slack.GetConversationHistoryResponse
	err := retry.Do(
		func() error {
			var err error
			result, err = c.api.GetConversationHistoryContext(ctx, params)
			if err != nil {
				if isRateLimitError(err) {
					return err // Retry
				}
				if strings.Contains(err.Error(), "channel_not_found") {
					return retry.Unrecoverable(err)
				}
				return err // Retry other errors
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Debug("GetChannelHistory failed with detailed error info",
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"error_string", err.Error(),
			"channel_id", channelID,
			"oldest", oldest,
			"latest", latest,
			"limit", limit,
			"api_method", "GetConversationHistoryContext")
	}
	return result, err
}

// BotInfo returns information about the authenticated bot user (cached for 30 minutes).
func (c *Client) BotInfo(ctx context.Context) (*slack.AuthTestResponse, error) {
	cacheKey := "bot_auth_test"

	// Try cache first
	if cached, found := c.cache.get(cacheKey); found {
		if authTest, ok := cached.(*slack.AuthTestResponse); ok {
			slog.Debug("using cached bot auth info")
			return authTest, nil
		}
		slog.Warn("cached auth test has incorrect type, refreshing")
		c.cache.invalidate(cacheKey)
	}

	// Fetch from API
	slog.Debug("fetching bot auth info from Slack API")
	var authTest *slack.AuthTestResponse
	err := retry.Do(
		func() error {
			var err error
			authTest, err = c.api.AuthTestContext(ctx)
			if err != nil {
				if isRateLimitError(err) {
					return err // Retry
				}
				return retry.Unrecoverable(err) // Don't retry auth errors
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return nil, err
	}

	// Cache for 1 hour (invalidated by member events for setup responsiveness)
	c.cache.set(cacheKey, authTest, time.Hour)
	slog.Debug("cached bot auth info", "bot_user_id", authTest.UserID, "bot_user", authTest.User)

	return authTest, nil
}

// ResolveChannelID resolves a channel name (e.g., "all-codegroove" or "#all-codegroove") to a channel ID.
// Returns the channel ID if found, or the original input if it's already an ID or can't be resolved.
func (c *Client) ResolveChannelID(ctx context.Context, channelName string) string {
	originalChannelName := channelName
	slog.Debug("attempting to resolve channel name to ID",
		"input", originalChannelName)

	// If it's already a channel ID (starts with C), return as-is
	if channelName != "" && channelName[0] == 'C' {
		slog.Debug("input is already a channel ID", "channel_id", channelName)
		return channelName
	}

	// Remove # prefix if present
	if channelName != "" && channelName[0] == '#' {
		channelName = channelName[1:]
		slog.Debug("removed # prefix", "original", originalChannelName, "cleaned", channelName)
	}

	// Check cache first (very important for performance)
	cacheKey := fmt.Sprintf("channel_resolution_%s", channelName)
	if cached, found := c.cache.get(cacheKey); found {
		if resolvedID, ok := cached.(string); ok {
			slog.Debug("using cached channel resolution", "name", channelName, "id", resolvedID)
			return resolvedID
		}
		slog.Warn("cached channel resolution has incorrect type, refreshing")
		c.cache.invalidate(cacheKey)
	}

	slog.Debug("channel not in cache, fetching from Slack API", "channel", channelName)

	// Try to find the channel - first try public and private channels
	channels, cursor, err := c.fetchConversationsWithRetry(ctx, []string{"public_channel", "private_channel"}, "")
	if err != nil {
		slog.Warn("failed to get public+private conversations, trying public only",
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"channel", channelName)

		// Fallback: try public channels only (might not have private channel permissions)
		channels, cursor, err = c.fetchConversationsWithRetry(ctx, []string{"public_channel"}, "")
		if err != nil {
			slog.Error("failed to get conversations for channel resolution",
				"error", err,
				"error_type", fmt.Sprintf("%T", err),
				"error_string", err.Error(),
				"channel", channelName,
				"attempted_types", []string{"public_channel", "private_channel"},
				"fallback_types", []string{"public_channel"},
				"api_method", "GetConversationsContext")
			return channelName // Return original name if we can't resolve
		}
		slog.Debug("successfully retrieved public channels only", "channel", channelName, "count", len(channels))
	}

	// Search through initial page
	if resolvedID, found := c.searchChannelsForName(channels, channelName, cacheKey); found {
		return resolvedID
	}

	// If we have more pages, search them too
	for cursor != "" {
		channels, cursor, err = c.fetchConversationsWithRetry(ctx, []string{"public_channel", "private_channel"}, cursor)
		if err != nil {
			slog.Warn("failed to get additional conversations for channel resolution", "error", err)
			break
		}

		if resolvedID, found := c.searchChannelsForName(channels, channelName, cacheKey); found {
			return resolvedID
		}
	}

	slog.Warn("could not resolve channel name to ID",
		"channel", channelName,
		"team_id", c.teamID)
	// Cache the failure for SHORT time (user might create channel or fix typo)
	c.cache.set(cacheKey, channelName, 45*time.Second)
	slog.Info("caching channel resolution failure briefly to allow for channel creation",
		"channel", channelName, "cache_ttl", "45s")
	return channelName // Return original if not found
}

// fetchConversationsWithRetry fetches conversations with retry logic.
func (c *Client) fetchConversationsWithRetry(ctx context.Context, types []string, cursor string) ([]slack.Channel, string, error) {
	var channels []slack.Channel
	var nextCursor string

	err := retry.Do(
		func() error {
			var err error
			channels, nextCursor, err = c.api.GetConversationsContext(ctx, &slack.GetConversationsParameters{
				Types:  types,
				Limit:  200,
				Cursor: cursor,
			})
			if err != nil {
				if isRateLimitError(err) {
					return err // Retry
				}
				return retry.Unrecoverable(err) // Don't retry permission errors
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)

	return channels, nextCursor, err
}

// searchChannelsForName searches a list of channels for a matching name and caches the result.
// Returns (channelID, found).
func (c *Client) searchChannelsForName(channels []slack.Channel, channelName, cacheKey string) (string, bool) {
	for i := range channels {
		if channels[i].Name == channelName {
			slog.Debug("resolved channel name to ID", "name", channelName, "id", channels[i].ID)
			// Cache the successful resolution for 1 hour (channels are stable, invalidated by events)
			c.cache.set(cacheKey, channels[i].ID, time.Hour)
			return channels[i].ID, true
		}
	}
	return "", false
}

// IsUserInChannel checks if a specific user is a member of the specified channel.
func (c *Client) IsUserInChannel(ctx context.Context, channelID, userID string) bool {
	// Check cache first
	cacheKey := fmt.Sprintf("user_in_channel_%s_%s", channelID, userID)
	if cached, found := c.cache.get(cacheKey); found {
		if isMember, ok := cached.(bool); ok {
			slog.Debug("using cached user channel membership", "channel_id", channelID, "user_id", userID, "is_member", isMember)
			return isMember
		}
		slog.Warn("cached user channel membership has incorrect type, refreshing")
		c.cache.invalidate(cacheKey)
	}

	slog.Debug("user channel membership not cached, checking via API", logFieldChannelID, channelID, "user_id", userID)

	// Get channel members
	var members []string
	err := retry.Do(
		func() error {
			var err error
			members, _, err = c.api.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
				ChannelID: channelID,
				Limit:     200,
			})
			if err != nil {
				if isRateLimitError(err) {
					return err // Retry
				}
				// Don't retry channel_not_found or not_in_channel
				if strings.Contains(err.Error(), "channel_not_found") || strings.Contains(err.Error(), "not_in_channel") {
					return retry.Unrecoverable(err)
				}
				return err // Retry other errors
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Warn("failed to get channel members for user membership check",
			"error", err,
			logFieldChannelID, channelID,
			"user_id", userID)
		return false
	}

	// Check if user ID is in the members list
	for _, member := range members {
		if member == userID {
			slog.Debug("user is a member of channel", "channel_id", channelID, "user_id", userID)
			// Cache positive result for 5 minutes (users join/leave channels frequently)
			c.cache.set(cacheKey, true, 5*time.Minute)
			return true
		}
	}

	slog.Debug("user is not a member of channel", "channel_id", channelID, "user_id", userID)
	// Cache negative result for SHORT time (user might be added)
	c.cache.set(cacheKey, false, 1*time.Minute)
	return false
}

// IsBotInChannel checks if the bot is a member of the specified channel with adaptive caching.
func (c *Client) IsBotInChannel(ctx context.Context, channelID string) bool {
	// Check cache first
	cacheKey := fmt.Sprintf("bot_in_channel_%s", channelID)
	if cached, found := c.cache.get(cacheKey); found {
		if isMember, ok := cached.(bool); ok {
			slog.Debug("using cached channel membership", "channel_id", channelID, "is_member", isMember)
			return isMember
		}
		slog.Warn("cached channel membership has incorrect type, refreshing")
		c.cache.invalidate(cacheKey)
	}

	slog.Debug("channel membership not cached, checking via API", logFieldChannelID, channelID)

	// Get bot user info first (this is now cached)
	authTest, err := c.BotInfo(ctx)
	if err != nil {
		slog.Error("failed to get bot user info for channel membership check",
			"error", err,
			logFieldChannelID, channelID)
		return false
	}

	// Get channel members
	var members []string
	err = retry.Do(
		func() error {
			var err error
			members, _, err = c.api.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
				ChannelID: channelID,
				Limit:     200,
			})
			if err != nil {
				if isRateLimitError(err) {
					return err // Retry
				}
				// Don't retry channel_not_found or not_in_channel - these are user errors
				if strings.Contains(err.Error(), "channel_not_found") || strings.Contains(err.Error(), "not_in_channel") {
					return retry.Unrecoverable(err)
				}
				return err // Retry other errors
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(c.getRetryDelay()),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		// Check if it's a channel not found error
		if strings.Contains(err.Error(), "channel_not_found") {
			slog.Info("channel does not exist",
				"channel_id", channelID,
				"action_required", "create channel or check channel name")
			// Cache that channel doesn't exist for SHORT time (user might create it)
			c.cache.set(cacheKey, false, 1*time.Minute)
			slog.Info("caching channel not found briefly to allow for channel creation",
				"channel_id", channelID, "cache_ttl", "1m")
			return false
		}
		// Check if it's a not_in_channel error
		if strings.Contains(err.Error(), "not_in_channel") {
			slog.Debug("bot is not a member of channel",
				"channel_id", channelID,
				"bot_user_id", authTest.UserID,
				"action_required", "invite bot to channel")
			// Cache negative result for SHORT time (user likely to fix quickly)
			c.cache.set(cacheKey, false, 15*time.Second)
			slog.Debug("caching bot not in channel briefly to allow quick retry after invite",
				logFieldChannelID, channelID, "cache_ttl", "15s")
			return false
		}
		slog.Warn("failed to get channel members - unknown error",
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"error_string", err.Error(),
			logFieldChannelID, channelID)
		return false
	}

	// Check if bot user ID is in the members list
	for _, member := range members {
		if member == authTest.UserID {
			slog.Debug("bot is a member of channel", "channel_id", channelID, "bot_user_id", authTest.UserID)
			// Cache positive result for 1 hour (stable membership, invalidated by member events)
			c.cache.set(cacheKey, true, time.Hour)
			return true
		}
	}

	slog.Debug("bot is not a member of channel",
		"channel_id", channelID,
		"bot_user_id", authTest.UserID,
		"total_members", len(members),
		"action_required", "invite bot to channel")
	// Cache negative result for SHORT time (user likely to fix this quickly)
	c.cache.set(cacheKey, false, 20*time.Second)
	slog.Debug("caching bot membership failure briefly to allow quick retry after user fixes issue",
		logFieldChannelID, channelID, "cache_ttl", "20s")
	return false
}
