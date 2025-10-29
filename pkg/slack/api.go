package slack

import (
	"context"

	"github.com/slack-go/slack"
)

// SlackAPI defines the interface for Slack API operations.
// This abstraction allows for easier testing by enabling mock implementations.
type SlackAPI interface {
	// Team operations.
	GetTeamInfoContext(ctx context.Context) (*slack.TeamInfo, error)
	AuthTestContext(ctx context.Context) (*slack.AuthTestResponse, error)

	// Conversation operations.
	GetConversationInfoContext(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error)
	GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationsContext(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error)
	OpenConversationContext(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error)
	GetUsersInConversationContext(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error)

	// Message operations.
	PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error)
	UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
	SearchMessagesContext(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error)

	// Reaction operations.
	AddReactionContext(ctx context.Context, name string, item slack.ItemRef) error
	RemoveReactionContext(ctx context.Context, name string, item slack.ItemRef) error

	// User operations.
	GetUserInfoContext(ctx context.Context, userID string) (*slack.User, error)
	GetUserPresenceContext(ctx context.Context, userID string) (*slack.UserPresence, error)

	// View operations.
	PublishViewContext(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error)
}

// slackAPIWrapper wraps the real Slack client to implement SlackAPI interface.
type slackAPIWrapper struct {
	client *slack.Client
}

// newSlackAPIWrapper creates a new wrapper around the Slack client.
func newSlackAPIWrapper(client *slack.Client) SlackAPI {
	return &slackAPIWrapper{client: client}
}

// RawClient returns the underlying *slack.Client for compatibility.
// This should only be used when integrating with code that hasn't been
// refactored to use the SlackAPI interface yet.
func (w *slackAPIWrapper) RawClient() *slack.Client {
	return w.client
}

// Team operations.

func (w *slackAPIWrapper) GetTeamInfoContext(ctx context.Context) (*slack.TeamInfo, error) {
	return w.client.GetTeamInfoContext(ctx)
}

func (w *slackAPIWrapper) AuthTestContext(ctx context.Context) (*slack.AuthTestResponse, error) {
	return w.client.AuthTestContext(ctx)
}

// Conversation operations.

func (w *slackAPIWrapper) GetConversationInfoContext(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
	return w.client.GetConversationInfoContext(ctx, input)
}

//nolint:revive // line length acceptable for API wrapper signature
func (w *slackAPIWrapper) GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	return w.client.GetConversationHistoryContext(ctx, params)
}

func (w *slackAPIWrapper) GetConversationsContext(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
	return w.client.GetConversationsContext(ctx, params)
}

//nolint:gocritic,revive // matches Slack API signature
func (w *slackAPIWrapper) OpenConversationContext(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
	return w.client.OpenConversationContext(ctx, params)
}

//nolint:gocritic,revive // line length acceptable for API wrapper signature
func (w *slackAPIWrapper) GetUsersInConversationContext(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
	return w.client.GetUsersInConversationContext(ctx, params)
}

// Message operations.

//nolint:gocritic,revive // matches Slack API signature
func (w *slackAPIWrapper) PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
	return w.client.PostMessageContext(ctx, channelID, options...)
}

//nolint:gocritic,revive // line length acceptable for API wrapper signature
func (w *slackAPIWrapper) UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	return w.client.UpdateMessageContext(ctx, channelID, timestamp, options...)
}

func (w *slackAPIWrapper) SearchMessagesContext(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error) {
	return w.client.SearchMessagesContext(ctx, query, params)
}

// Reaction operations.

func (w *slackAPIWrapper) AddReactionContext(ctx context.Context, name string, item slack.ItemRef) error {
	return w.client.AddReactionContext(ctx, name, item)
}

func (w *slackAPIWrapper) RemoveReactionContext(ctx context.Context, name string, item slack.ItemRef) error {
	return w.client.RemoveReactionContext(ctx, name, item)
}

// User operations.

func (w *slackAPIWrapper) GetUserInfoContext(ctx context.Context, userID string) (*slack.User, error) {
	return w.client.GetUserInfoContext(ctx, userID)
}

func (w *slackAPIWrapper) GetUserPresenceContext(ctx context.Context, userID string) (*slack.UserPresence, error) {
	return w.client.GetUserPresenceContext(ctx, userID)
}

// View operations.

func (w *slackAPIWrapper) PublishViewContext(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
	return w.client.PublishViewContext(ctx, request)
}
