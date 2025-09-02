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
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// Client wraps the Slack API client.
type Client struct {
	api           *slack.Client
	signingSecret string
}

// New creates a new Slack client.
func New(token, signingSecret string) *Client {
	return &Client{
		api:           slack.New(token),
		signingSecret: signingSecret,
	}
}

// PostThread creates a new thread in a channel for a PR with retry logic.
func (c *Client) PostThread(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
	slog.Info("posting thread to channel", "channel", channelID)

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

	slog.Info("successfully posted thread", "thread", timestamp, "channel", channelID)
	return timestamp, nil
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
	err := retry.Do(
		func() error {
			err := c.api.AddReactionContext(ctx, emoji, slack.ItemRef{
				Channel:   channelID,
				Timestamp: timestamp,
			})
			if err != nil {
				// Ignore "already_reacted" errors - not really an error
				if strings.Contains(err.Error(), "already_reacted") {
					return nil
				}
				if isRateLimitError(err) {
					slog.Debug("rate limited adding reaction, backing off", "emoji", emoji)
					return err
				}
				// Don't retry on message_not_found
				if strings.Contains(err.Error(), "message_not_found") ||
					strings.Contains(err.Error(), "no_reaction") {
					return retry.Unrecoverable(err)
				}
				slog.Debug("failed to add reaction, retrying", "emoji", emoji, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(10*time.Second),
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
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(10*time.Second),
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
func (c *Client) UpdateReactions(ctx context.Context, channelID, timestamp, newState string) error {
	// Map states to emojis.
	stateEmojis := map[string]string{
		"test_tube":     "test_tube",
		"broken_heart":  "broken_heart",
		"hourglass":     "hourglass",
		"carpentry_saw": "carpentry_saw",
		"check":         "white_check_mark",
		"pray":          "pray",
		"face_palm":     "face_palm",
	}

	// Remove all existing reactions.
	for _, emoji := range stateEmojis {
		if err := c.RemoveReaction(ctx, channelID, timestamp, emoji); err != nil {
			// Log but don't fail - reaction might not exist.
			slog.Warn("failed to remove reaction", "emoji", emoji, "error", err)
		}
	}

	// Add new reaction.
	if emoji, ok := stateEmojis[newState]; ok {
		return c.AddReaction(ctx, channelID, timestamp, emoji)
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
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
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
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
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
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(10*time.Second),
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
func (c *Client) EventsHandler(w http.ResponseWriter, r *http.Request) {
	// Read body for verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify the signature.
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	if !c.verifySignature(signature, timestamp, body) {
		slog.Warn("failed to verify signature")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	eventsAPIEvent, err := slackevents.ParseEvent(body, slackevents.OptionNoVerifyToken())
	if err != nil {
		slog.Warn("failed to parse Slack event", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Handle URL verification.
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var challenge slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &challenge); err != nil {
			slog.Error("failed to unmarshal challenge", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(challenge.Challenge)); err != nil {
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
		}
	}

	w.WriteHeader(http.StatusOK)
}

// InteractionsHandler handles Slack interactive components.
func (c *Client) InteractionsHandler(w http.ResponseWriter, r *http.Request) {
	// Parse the payload.
	payload := r.FormValue("payload")
	if payload == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var interaction slack.InteractionCallback
	if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
		slog.Error("failed to unmarshal interaction", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify the request signature.
	if !c.verifyRequest(r) {
		w.WriteHeader(http.StatusUnauthorized)
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

	w.WriteHeader(http.StatusOK)
}

// SlashCommandHandler handles Slack slash commands.
func (c *Client) SlashCommandHandler(w http.ResponseWriter, r *http.Request) {
	// Verify the request signature.
	if !c.verifyRequest(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Parse the command.
	cmd, err := slack.SlashCommandParse(r)
	if err != nil {
		slog.Error("failed to parse slash command", "error", err)
		w.WriteHeader(http.StatusBadRequest)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"text": response,
	}); err != nil {
		slog.Error("failed to encode slash command response", "error", err)
	}
}

// handleR2RCommand handles the /r2r slash command.
func (*Client) handleR2RCommand(cmd *slack.SlashCommand) string {
	// Sanitize and validate input.
	text := strings.TrimSpace(cmd.Text)
	if len(text) > 200 { // Reasonable limit for command input.
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
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
	)
	if err != nil {
		return fmt.Errorf("failed to publish home view after retries: %w", err)
	}
	return nil
}
