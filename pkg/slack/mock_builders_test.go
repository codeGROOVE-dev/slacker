package slack

import (
	"context"
	"errors"

	"github.com/slack-go/slack"
)

// MockAPIBuilder provides a fluent API for building mockAPI instances.
// This makes test setup much more readable and maintainable.
//
// Example:
//
//	mockAPI := NewMockAPI().
//		WithPostMessageSuccess("C123", "1234.567").
//		WithGetTeamInfo(&slack.TeamInfo{Domain: "test"}).
//		Build()
type MockAPIBuilder struct {
	mock *mockAPI
}

// NewMockAPI creates a new mock Slack API builder with sensible defaults.
func NewMockAPI() *MockAPIBuilder {
	return &MockAPIBuilder{
		mock: &mockAPI{},
	}
}

// WithPostMessageSuccess configures the mock to successfully post messages.
func (b *MockAPIBuilder) WithPostMessageSuccess(channelID, timestamp string) *MockAPIBuilder {
	b.mock.postMessageFunc = func(ctx context.Context, cid string, options ...slack.MsgOption) (string, string, error) {
		return channelID, timestamp, nil
	}
	return b
}

// WithPostMessageError configures the mock to fail when posting messages.
func (b *MockAPIBuilder) WithPostMessageError(err error) *MockAPIBuilder {
	b.mock.postMessageFunc = func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
		return "", "", err
	}
	return b
}

// WithUpdateMessageSuccess configures the mock to successfully update messages.
func (b *MockAPIBuilder) WithUpdateMessageSuccess() *MockAPIBuilder {
	b.mock.updateMessageFunc = func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
		return channelID, timestamp, "", nil
	}
	return b
}

// WithUpdateMessageError configures the mock to fail when updating messages.
func (b *MockAPIBuilder) WithUpdateMessageError(err error) *MockAPIBuilder {
	b.mock.updateMessageFunc = func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
		return "", "", "", err
	}
	return b
}

// WithGetTeamInfo configures the team info returned by the mock.
func (b *MockAPIBuilder) WithGetTeamInfo(info *slack.TeamInfo) *MockAPIBuilder {
	b.mock.getTeamInfoFunc = func(ctx context.Context) (*slack.TeamInfo, error) {
		return info, nil
	}
	return b
}

// WithGetTeamInfoError configures the mock to fail when getting team info.
func (b *MockAPIBuilder) WithGetTeamInfoError(err error) *MockAPIBuilder {
	b.mock.getTeamInfoFunc = func(ctx context.Context) (*slack.TeamInfo, error) {
		return nil, err
	}
	return b
}

// WithAuthTestSuccess configures the mock to successfully authenticate.
func (b *MockAPIBuilder) WithAuthTestSuccess(userID, teamID string) *MockAPIBuilder {
	b.mock.authTestFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{
			UserID: userID,
			TeamID: teamID,
		}, nil
	}
	return b
}

// WithAuthTestError configures the mock to fail authentication.
func (b *MockAPIBuilder) WithAuthTestError(err error) *MockAPIBuilder {
	b.mock.authTestFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return nil, err
	}
	return b
}

// WithGetConversationInfo configures the conversation info returned by the mock.
func (b *MockAPIBuilder) WithGetConversationInfo(channel *slack.Channel) *MockAPIBuilder {
	b.mock.getConversationInfoFunc = func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
		return channel, nil
	}
	return b
}

// WithGetConversationInfoError configures the mock to fail when getting conversation info.
func (b *MockAPIBuilder) WithGetConversationInfoError(err error) *MockAPIBuilder {
	b.mock.getConversationInfoFunc = func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
		return nil, err
	}
	return b
}

// WithGetConversationHistory configures the conversation history returned by the mock.
func (b *MockAPIBuilder) WithGetConversationHistory(messages []slack.Message) *MockAPIBuilder {
	b.mock.getConversationHistoryFunc = func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{
			Messages: messages,
		}, nil
	}
	return b
}

// WithGetConversationHistoryError configures the mock to fail when getting conversation history.
func (b *MockAPIBuilder) WithGetConversationHistoryError(err error) *MockAPIBuilder {
	b.mock.getConversationHistoryFunc = func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
		return nil, err
	}
	return b
}

// WithGetUserInfo configures the user info returned by the mock.
func (b *MockAPIBuilder) WithGetUserInfo(user *slack.User) *MockAPIBuilder {
	b.mock.getUserInfoFunc = func(ctx context.Context, userID string) (*slack.User, error) {
		return user, nil
	}
	return b
}

// WithGetUserInfoError configures the mock to fail when getting user info.
func (b *MockAPIBuilder) WithGetUserInfoError(err error) *MockAPIBuilder {
	b.mock.getUserInfoFunc = func(ctx context.Context, userID string) (*slack.User, error) {
		return nil, err
	}
	return b
}

// WithGetUserPresence configures the user presence returned by the mock.
func (b *MockAPIBuilder) WithGetUserPresence(presence string) *MockAPIBuilder {
	b.mock.getUserPresenceFunc = func(ctx context.Context, userID string) (*slack.UserPresence, error) {
		return &slack.UserPresence{
			Presence: presence,
		}, nil
	}
	return b
}

// WithGetUserPresenceError configures the mock to fail when getting user presence.
func (b *MockAPIBuilder) WithGetUserPresenceError(err error) *MockAPIBuilder {
	b.mock.getUserPresenceFunc = func(ctx context.Context, userID string) (*slack.UserPresence, error) {
		return nil, err
	}
	return b
}

// WithOpenConversation configures the conversation returned when opening a DM.
func (b *MockAPIBuilder) WithOpenConversation(channel *slack.Channel) *MockAPIBuilder {
	b.mock.openConversationFunc = func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
		return channel, false, false, nil
	}
	return b
}

// WithOpenConversationError configures the mock to fail when opening conversations.
func (b *MockAPIBuilder) WithOpenConversationError(err error) *MockAPIBuilder {
	b.mock.openConversationFunc = func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
		return nil, false, false, err
	}
	return b
}

// WithSearchMessages configures the search results returned by the mock.
func (b *MockAPIBuilder) WithSearchMessages(messages *slack.SearchMessages) *MockAPIBuilder {
	b.mock.searchMessagesFunc = func(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error) {
		return messages, nil
	}
	return b
}

// WithSearchMessagesError configures the mock to fail when searching messages.
func (b *MockAPIBuilder) WithSearchMessagesError(err error) *MockAPIBuilder {
	b.mock.searchMessagesFunc = func(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error) {
		return nil, err
	}
	return b
}

// WithGetUsersInConversation configures the users in a conversation.
func (b *MockAPIBuilder) WithGetUsersInConversation(users []string) *MockAPIBuilder {
	b.mock.getUsersInConversationFunc = func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
		return users, "", nil
	}
	return b
}

// WithGetUsersInConversationError configures the mock to fail when getting users in conversation.
func (b *MockAPIBuilder) WithGetUsersInConversationError(err error) *MockAPIBuilder {
	b.mock.getUsersInConversationFunc = func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
		return nil, "", err
	}
	return b
}

// WithGetConversations configures the conversations returned by the mock.
func (b *MockAPIBuilder) WithGetConversations(channels []slack.Channel) *MockAPIBuilder {
	b.mock.getConversationsFunc = func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
		return channels, "", nil
	}
	return b
}

// WithGetConversationsError configures the mock to fail when getting conversations.
func (b *MockAPIBuilder) WithGetConversationsError(err error) *MockAPIBuilder {
	b.mock.getConversationsFunc = func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
		return nil, "", err
	}
	return b
}

// WithPublishViewSuccess configures the mock to successfully publish views.
func (b *MockAPIBuilder) WithPublishViewSuccess() *MockAPIBuilder {
	b.mock.publishViewFunc = func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
		return &slack.ViewResponse{}, nil
	}
	return b
}

// WithPublishViewError configures the mock to fail when publishing views.
func (b *MockAPIBuilder) WithPublishViewError(err error) *MockAPIBuilder {
	b.mock.publishViewFunc = func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
		return nil, err
	}
	return b
}

// WithUserTimezone configures the user timezone returned by the mock.
func (b *MockAPIBuilder) WithUserTimezone(timezone string) *MockAPIBuilder {
	// UserTimezone is a Client method, not an API method
	// We'll need to handle this at the Client level in tests
	return b
}

// Build returns the configured mockAPI.
func (b *MockAPIBuilder) Build() *mockAPI {
	return b.mock
}

// Common error types for testing
var (
	ErrAPIError         = errors.New("api error")
	ErrNotFound         = errors.New("not found")
	ErrPermissionDenied = errors.New("permission_denied")
	ErrRateLimited      = errors.New("rate_limited")
)
