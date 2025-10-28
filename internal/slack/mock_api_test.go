package slack

import (
	"context"
	"errors"

	"github.com/slack-go/slack"
)

// mockSlackAPI implements SlackAPI for testing.
type mockSlackAPI struct {
	// Team operations
	getTeamInfoFunc   func(ctx context.Context) (*slack.TeamInfo, error)
	authTestFunc      func(ctx context.Context) (*slack.AuthTestResponse, error)

	// Conversation operations
	getConversationInfoFunc        func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error)
	getConversationHistoryFunc     func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	getConversationsFunc           func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error)
	openConversationFunc           func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error)
	getUsersInConversationFunc     func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error)

	// Message operations
	postMessageFunc   func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error)
	updateMessageFunc func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
	searchMessagesFunc func(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error)

	// Reaction operations
	addReactionFunc    func(ctx context.Context, name string, item slack.ItemRef) error
	removeReactionFunc func(ctx context.Context, name string, item slack.ItemRef) error

	// User operations
	getUserInfoFunc     func(ctx context.Context, userID string) (*slack.User, error)
	getUserPresenceFunc func(ctx context.Context, userID string) (*slack.UserPresence, error)

	// View operations
	publishViewFunc func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error)
}

// Team operations

func (m *mockSlackAPI) GetTeamInfoContext(ctx context.Context) (*slack.TeamInfo, error) {
	if m.getTeamInfoFunc != nil {
		return m.getTeamInfoFunc(ctx)
	}
	return nil, errors.New("not implemented")
}

func (m *mockSlackAPI) AuthTestContext(ctx context.Context) (*slack.AuthTestResponse, error) {
	if m.authTestFunc != nil {
		return m.authTestFunc(ctx)
	}
	return nil, errors.New("not implemented")
}

// Conversation operations

func (m *mockSlackAPI) GetConversationInfoContext(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
	if m.getConversationInfoFunc != nil {
		return m.getConversationInfoFunc(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (m *mockSlackAPI) GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	if m.getConversationHistoryFunc != nil {
		return m.getConversationHistoryFunc(ctx, params)
	}
	return nil, errors.New("not implemented")
}

func (m *mockSlackAPI) GetConversationsContext(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
	if m.getConversationsFunc != nil {
		return m.getConversationsFunc(ctx, params)
	}
	return nil, "", errors.New("not implemented")
}

func (m *mockSlackAPI) OpenConversationContext(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
	if m.openConversationFunc != nil {
		return m.openConversationFunc(ctx, params)
	}
	return nil, false, false, errors.New("not implemented")
}

func (m *mockSlackAPI) GetUsersInConversationContext(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
	if m.getUsersInConversationFunc != nil {
		return m.getUsersInConversationFunc(ctx, params)
	}
	return nil, "", errors.New("not implemented")
}

// Message operations

func (m *mockSlackAPI) PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
	if m.postMessageFunc != nil {
		return m.postMessageFunc(ctx, channelID, options...)
	}
	return "", "", errors.New("not implemented")
}

func (m *mockSlackAPI) UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	if m.updateMessageFunc != nil {
		return m.updateMessageFunc(ctx, channelID, timestamp, options...)
	}
	return "", "", "", errors.New("not implemented")
}

func (m *mockSlackAPI) SearchMessagesContext(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error) {
	if m.searchMessagesFunc != nil {
		return m.searchMessagesFunc(ctx, query, params)
	}
	return nil, errors.New("not implemented")
}

// Reaction operations

func (m *mockSlackAPI) AddReactionContext(ctx context.Context, name string, item slack.ItemRef) error {
	if m.addReactionFunc != nil {
		return m.addReactionFunc(ctx, name, item)
	}
	return errors.New("not implemented")
}

func (m *mockSlackAPI) RemoveReactionContext(ctx context.Context, name string, item slack.ItemRef) error {
	if m.removeReactionFunc != nil {
		return m.removeReactionFunc(ctx, name, item)
	}
	return errors.New("not implemented")
}

// User operations

func (m *mockSlackAPI) GetUserInfoContext(ctx context.Context, userID string) (*slack.User, error) {
	if m.getUserInfoFunc != nil {
		return m.getUserInfoFunc(ctx, userID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockSlackAPI) GetUserPresenceContext(ctx context.Context, userID string) (*slack.UserPresence, error) {
	if m.getUserPresenceFunc != nil {
		return m.getUserPresenceFunc(ctx, userID)
	}
	return nil, errors.New("not implemented")
}

// View operations

func (m *mockSlackAPI) PublishViewContext(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
	if m.publishViewFunc != nil {
		return m.publishViewFunc(ctx, request)
	}
	return nil, errors.New("not implemented")
}
