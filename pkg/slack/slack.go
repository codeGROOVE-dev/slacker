// Package slack provides a Slack API client and interaction handlers.
package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

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
type Client struct {
	api           *slack.Client
	cache         *apiCache
	signingSecret string
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
func (c *apiCache) get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, exists := c.entries[key]
	if !exists || time.Now().After(entry.expiresAt) {
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

// New creates a new Slack client with caching.
func New(token, signingSecret string) *Client {
	return &Client{
		api:           slack.New(token),
		signingSecret: signingSecret,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
}

// GetWorkspaceInfo returns information about the current workspace (cached for 1 hour).
func (c *Client) GetWorkspaceInfo(ctx context.Context) (*slack.TeamInfo, error) {
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
		retry.Delay(2*time.Second),
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
		retry.Delay(2*time.Second),
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
		retry.Delay(2*time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
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
		retry.Delay(2*time.Second),
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

// AddReaction adds a reaction emoji to a message with retry logic.
func (c *Client) AddReaction(ctx context.Context, channelID, timestamp, emoji string) error {
	slog.Debug("adding reaction to message",
		"channel_id", channelID,
		"timestamp", timestamp,
		"emoji", emoji)

	err := retry.Do(
		func() error {
			err := c.api.AddReactionContext(ctx, emoji, slack.ItemRef{
				Channel:   channelID,
				Timestamp: timestamp,
			})
			if err != nil {
				// Ignore "already_reacted" errors - not really an error
				if strings.Contains(err.Error(), "already_reacted") {
					slog.Debug("reaction already exists, skipping", "emoji", emoji)
					return nil
				}
				if isRateLimitError(err) {
					slog.Debug("rate limited adding reaction, backing off", "emoji", emoji)
					return err
				}
				// Don't retry on message_not_found
				if strings.Contains(err.Error(), "message_not_found") ||
					strings.Contains(err.Error(), "no_reaction") {
					slog.Error("permanent error adding reaction",
						"emoji", emoji,
						"error", err,
						"error_type", fmt.Sprintf("%T", err),
						"error_string", err.Error(),
						"channel_id", channelID,
						"timestamp", timestamp)
					return retry.Unrecoverable(err)
				}
				// Log detailed error info for any other failures
				slog.Warn("failed to add reaction, will retry",
					"emoji", emoji,
					"error", err,
					"error_type", fmt.Sprintf("%T", err),
					"error_string", err.Error(),
					"channel_id", channelID,
					"timestamp", timestamp)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to add reaction after retries: %w", err)
	}
	return nil
}

// RemoveReaction removes a reaction emoji from a message with retry logic.
func (c *Client) RemoveReaction(ctx context.Context, channelID, timestamp, emoji string) error {
	slog.Debug("removing reaction from message",
		"channel_id", channelID,
		"timestamp", timestamp,
		"emoji", emoji)

	err := retry.Do(
		func() error {
			err := c.api.RemoveReactionContext(ctx, emoji, slack.ItemRef{
				Channel:   channelID,
				Timestamp: timestamp,
			})
			if err != nil {
				// Ignore "no_reaction" errors - not really an error
				if strings.Contains(err.Error(), "no_reaction") {
					return nil
				}
				if isRateLimitError(err) {
					slog.Debug("rate limited removing reaction, backing off", "emoji", emoji)
					return err
				}
				// Don't retry on message_not_found
				if strings.Contains(err.Error(), "message_not_found") {
					return retry.Unrecoverable(err)
				}
				slog.Debug("failed to remove reaction, retrying", "emoji", emoji, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to remove reaction after retries: %w", err)
	}
	return nil
}

// UpdateReactions updates the reaction on a message based on PR state.
// This is optimized to only add the desired reaction without removing all others first.
func (c *Client) UpdateReactions(ctx context.Context, channelID, timestamp, newState string) error {
	// Map states to emojis.
	stateEmojis := map[string]string{
		"test_tube":     "test_tube",
		"broken_heart":  "broken_heart",
		"hourglass":     "hourglass",
		"carpentry_saw": "carpentry_saw",
		"check":         "white_check_mark",
		"merged":        "rocket",
		"face_palm":     "face_palm",
	}

	// Simply add the new reaction for the current state.
	// The Slack API will ignore duplicate reactions (already_reacted error is handled in AddReaction).
	// We rely on the bot logic to not call this unnecessarily for the same state.
	if emoji, ok := stateEmojis[newState]; ok {
		return c.AddReaction(ctx, channelID, timestamp, emoji)
	}

	return nil
}

// UpdateReactionsWithPrevious updates the reaction on a message, removing the old state reaction and adding the new one.
// This method is more efficient when you know the previous state.
func (c *Client) UpdateReactionsWithPrevious(ctx context.Context, channelID, timestamp, oldState, newState string) error {
	// Map states to emojis.
	stateEmojis := map[string]string{
		"test_tube":     "test_tube",
		"broken_heart":  "broken_heart",
		"hourglass":     "hourglass",
		"carpentry_saw": "carpentry_saw",
		"check":         "white_check_mark",
		"merged":        "rocket",
		"face_palm":     "face_palm",
	}

	// Remove old reaction if it exists and is different from new state
	if oldState != "" && oldState != newState {
		if oldEmoji, ok := stateEmojis[oldState]; ok {
			if err := c.RemoveReaction(ctx, channelID, timestamp, oldEmoji); err != nil {
				// Log but don't fail - reaction might not exist
				slog.Debug("failed to remove old reaction", "emoji", oldEmoji, "error", err)
			}
		}
	}

	// Add new reaction
	if newEmoji, ok := stateEmojis[newState]; ok {
		return c.AddReaction(ctx, channelID, timestamp, newEmoji)
	}

	return nil
}

// SendDirectMessage sends a direct message to a user with retry logic.
func (c *Client) SendDirectMessage(ctx context.Context, userID, text string) error {
	slog.Info("sending DM to user", "user", userID)

	var channelID string

	// First, open conversation with retry
	err := retry.Do(
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
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to open conversation after retries: %w", err)
	}

	// Then send message with retry
	err = retry.Do(
		func() error {
			_, _, err := c.api.PostMessageContext(ctx, channelID, slack.MsgOptionText(text, false))
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited sending DM, backing off", "user", userID)
					return err
				}
				slog.Warn("failed to send DM, retrying", "user", userID, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(2*time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to send DM after retries: %w", err)
	}

	slog.Info("successfully sent DM", "user", userID)
	return nil
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
		retry.Delay(time.Second),
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
		retry.Delay(time.Second),
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
	if !c.verifySignature(signature, timestamp, body) {
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
			// Update app home when user opens it.
			// In a full implementation, this would update the home tab.
			// For now, just log.
			slog.Debug("would update app home for user", "user", evt.User)
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
		}
	}

	writer.WriteHeader(http.StatusOK)
}

// InteractionsHandler handles Slack interactive components.
func (c *Client) InteractionsHandler(writer http.ResponseWriter, r *http.Request) {
	// Parse the payload.
	payload := r.FormValue("payload")
	if payload == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	var interaction slack.InteractionCallback
	if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
		slog.Error("failed to unmarshal interaction", "error", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify the request signature.
	if !c.verifyRequest(r) {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Handle different interaction types.
	switch interaction.Type {
	case slack.InteractionTypeBlockActions:
		// Handle block actions (buttons, selects, etc.).
		slog.Debug("received block action", "interaction", interaction)
	case slack.InteractionTypeViewSubmission:
		// Handle modal submissions.
		slog.Debug("received view submission", "interaction", interaction)
	default:
		// Other interaction types
		slog.Debug("unhandled interaction type", "type", interaction.Type)
	}

	writer.WriteHeader(http.StatusOK)
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
	case "/r2r":
		response = c.handleR2RCommand(&cmd)
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

// handleR2RCommand handles the /r2r slash command.
func (*Client) handleR2RCommand(cmd *slack.SlashCommand) string {
	// Sanitize and validate input.
	text := strings.TrimSpace(cmd.Text)
	if len(text) > maxCommandInputLength { // Reasonable limit for command input.
		return "Command too long. Please use shorter commands."
	}

	args := strings.Fields(text)
	if len(args) == 0 {
		return "Usage: /r2r [dashboard|settings|help]"
	}

	// Validate command argument.
	command := strings.ToLower(args[0])
	switch command {
	case "dashboard":
		// Note: In a full implementation, we'd send blocks here instead of plain text.
		// For now, return a link to the web dashboard.
		return fmt.Sprintf("View your dashboard at: https://dash.ready-to-review.dev/?user=%s\n"+
			"Or use the Home tab in this app for the native Slack experience.", cmd.UserID)
	case "settings":
		return "Open the Home tab in this app to configure your notification preferences."
	case "help":
		return "Ready to Review helps you stay on top of pull requests.\n" +
			"Commands:\n" +
			"• /r2r dashboard - View your PR dashboard\n" +
			"• /r2r settings - Configure notification preferences\n" +
			"• /r2r help - Show this help message\n\n" +
			"You can also visit the Home tab in this app for a full dashboard."
	default:
		return "Unknown subcommand. Try: /r2r help"
	}
}

// verifySignature verifies a Slack request signature.
func (c *Client) verifySignature(signature, timestamp string, body []byte) bool {
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

	return c.verifySignature(signature, timestamp, body)
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
func (c *Client) PublishHomeView(userID string, blocks []slack.Block) error {
	view := slack.HomeTabViewRequest{
		Type:   "home",
		Blocks: slack.Blocks{BlockSet: blocks},
	}

	err := retry.Do(
		func() error {
			_, err := c.api.PublishView(userID, view, "")
			if err != nil {
				if isRateLimitError(err) {
					slog.Warn("rate limited publishing home view, backing off", "user", userID)
					return err
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
		retry.Attempts(5),
		retry.Delay(time.Second),
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
		retry.Delay(2*time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	return result, err
}

// API returns the underlying Slack API client.
func (c *Client) API() *slack.Client {
	return c.api
}

// GetChannelHistory retrieves channel message history with optional time filtering.
func (c *Client) GetChannelHistory(
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
		retry.Delay(2*time.Second),
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

// GetBotInfo returns information about the authenticated bot user (cached for 30 minutes).
func (c *Client) GetBotInfo(ctx context.Context) (*slack.AuthTestResponse, error) {
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
		retry.Delay(2*time.Second),
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
	var channels []slack.Channel
	var cursor string
	err := retry.Do(
		func() error {
			var err error
			channels, cursor, err = c.api.GetConversationsContext(ctx, &slack.GetConversationsParameters{
				Types: []string{"public_channel", "private_channel"},
				Limit: 200,
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
		retry.Delay(2*time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Warn("failed to get public+private conversations, trying public only",
			"error", err,
			"error_type", fmt.Sprintf("%T", err),
			"channel", channelName)

		// Fallback: try public channels only (might not have private channel permissions)
		err = retry.Do(
			func() error {
				var err error
				channels, cursor, err = c.api.GetConversationsContext(ctx, &slack.GetConversationsParameters{
					Types: []string{"public_channel"},
					Limit: 200,
				})
				if err != nil {
					if isRateLimitError(err) {
						return err // Retry
					}
					return retry.Unrecoverable(err)
				}
				return nil
			},
			retry.Attempts(5),
			retry.Delay(2*time.Second),
			retry.MaxDelay(2*time.Minute),
			retry.DelayType(retry.BackOffDelay),
			retry.MaxJitter(time.Second),
			retry.LastErrorOnly(true),
			retry.Context(ctx),
		)
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

	// Search through channels
	for i := range channels {
		channel := &channels[i]
		if channel.Name == channelName {
			slog.Debug("resolved channel name to ID", "name", channelName, "id", channel.ID)
			// Cache the successful resolution for 1 hour (channels are stable, invalidated by events)
			c.cache.set(cacheKey, channel.ID, time.Hour)
			return channel.ID
		}
	}

	// If we have more pages, search them too
	for cursor != "" {
		err = retry.Do(
			func() error {
				var err error
				channels, cursor, err = c.api.GetConversationsContext(ctx, &slack.GetConversationsParameters{
					Types:  []string{"public_channel", "private_channel"},
					Limit:  200,
					Cursor: cursor,
				})
				if err != nil {
					if isRateLimitError(err) {
						return err // Retry
					}
					return retry.Unrecoverable(err)
				}
				return nil
			},
			retry.Attempts(5),
			retry.Delay(2*time.Second),
			retry.MaxDelay(2*time.Minute),
			retry.DelayType(retry.BackOffDelay),
			retry.MaxJitter(time.Second),
			retry.LastErrorOnly(true),
			retry.Context(ctx),
		)
		if err != nil {
			slog.Warn("failed to get additional conversations for channel resolution", "error", err)
			break
		}

		for i := range channels {
			channel := &channels[i]
			if channel.Name == channelName {
				slog.Debug("resolved channel name to ID", "name", channelName, "id", channel.ID)
				// Cache the successful resolution for 1 hour (channels are stable, invalidated by events)
				c.cache.set(cacheKey, channel.ID, time.Hour)
				return channel.ID
			}
		}
	}

	slog.Warn("could not resolve channel name to ID", "channel", channelName)
	// Cache the failure for SHORT time (user might create channel or fix typo)
	c.cache.set(cacheKey, channelName, 45*time.Second)
	slog.Info("caching channel resolution failure briefly to allow for channel creation",
		"channel", channelName, "cache_ttl", "45s")
	return channelName // Return original if not found
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
	authTest, err := c.GetBotInfo(ctx)
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
		retry.Delay(2*time.Second),
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
			slog.Info("bot is not a member of channel",
				"channel_id", channelID,
				"bot_user_id", authTest.UserID,
				"action_required", "invite bot to channel")
			// Cache negative result for SHORT time (user likely to fix quickly)
			c.cache.set(cacheKey, false, 15*time.Second)
			slog.Info("caching bot not in channel briefly to allow quick retry after invite",
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

	slog.Info("bot is not a member of channel",
		"channel_id", channelID,
		"bot_user_id", authTest.UserID,
		"total_members", len(members),
		"action_required", "invite bot to channel")
	// Cache negative result for SHORT time (user likely to fix this quickly)
	c.cache.set(cacheKey, false, 20*time.Second)
	slog.Info("caching bot membership failure briefly to allow quick retry after user fixes issue",
		logFieldChannelID, channelID, "cache_ttl", "20s")
	return false
}
