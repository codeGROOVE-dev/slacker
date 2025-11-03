package slack

import (
	"context"
	"errors"

	"github.com/slack-go/slack"
)

// MockSlackAPIBuilder provides a fluent API for building mockSlackAPI instances.
// This makes test setup much more readable and maintainable.
//
// Example:
//
//	mockAPI := NewMockSlackAPI().
//		WithPostMessageSuccess("C123", "1234.567").
//		WithGetTeamInfo(&slack.TeamInfo{Domain: "test"}).
//		Build()
type MockSlackAPIBuilder struct {
	mock *mockSlackAPI
}

// NewMockSlackAPI creates a new mock Slack API builder with sensible defaults.
func NewMockSlackAPI() *MockSlackAPIBuilder {
	return &MockSlackAPIBuilder{
		mock: &mockSlackAPI{},
	}
}

// WithPostMessageSuccess configures the mock to successfully post messages.
func (b *MockSlackAPIBuilder) WithPostMessageSuccess(channelID, timestamp string) *MockSlackAPIBuilder {
	b.mock.postMessageFunc = func(ctx context.Context, cid string, options ...slack.MsgOption) (string, string, error) {
		return channelID, timestamp, nil
	}
	return b
}

// WithPostMessageError configures the mock to fail when posting messages.
func (b *MockSlackAPIBuilder) WithPostMessageError(err error) *MockSlackAPIBuilder {
	b.mock.postMessageFunc = func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
		return "", "", err
	}
	return b
}

// WithUpdateMessageSuccess configures the mock to successfully update messages.
func (b *MockSlackAPIBuilder) WithUpdateMessageSuccess() *MockSlackAPIBuilder {
	b.mock.updateMessageFunc = func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
		return channelID, timestamp, "", nil
	}
	return b
}

// WithUpdateMessageError configures the mock to fail when updating messages.
func (b *MockSlackAPIBuilder) WithUpdateMessageError(err error) *MockSlackAPIBuilder {
	b.mock.updateMessageFunc = func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
		return "", "", "", err
	}
	return b
}

// WithGetTeamInfo configures the team info returned by the mock.
func (b *MockSlackAPIBuilder) WithGetTeamInfo(info *slack.TeamInfo) *MockSlackAPIBuilder {
	b.mock.getTeamInfoFunc = func(ctx context.Context) (*slack.TeamInfo, error) {
		return info, nil
	}
	return b
}

// WithGetTeamInfoError configures the mock to fail when getting team info.
func (b *MockSlackAPIBuilder) WithGetTeamInfoError(err error) *MockSlackAPIBuilder {
	b.mock.getTeamInfoFunc = func(ctx context.Context) (*slack.TeamInfo, error) {
		return nil, err
	}
	return b
}

// WithAuthTestSuccess configures the mock to successfully authenticate.
func (b *MockSlackAPIBuilder) WithAuthTestSuccess(userID, teamID string) *MockSlackAPIBuilder {
	b.mock.authTestFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{
			UserID: userID,
			TeamID: teamID,
		}, nil
	}
	return b
}

// WithAuthTestError configures the mock to fail authentication.
func (b *MockSlackAPIBuilder) WithAuthTestError(err error) *MockSlackAPIBuilder {
	b.mock.authTestFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return nil, err
	}
	return b
}

// WithGetConversationInfo configures the conversation info returned by the mock.
func (b *MockSlackAPIBuilder) WithGetConversationInfo(channel *slack.Channel) *MockSlackAPIBuilder {
	b.mock.getConversationInfoFunc = func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
		return channel, nil
	}
	return b
}

// WithGetConversationInfoError configures the mock to fail when getting conversation info.
func (b *MockSlackAPIBuilder) WithGetConversationInfoError(err error) *MockSlackAPIBuilder {
	b.mock.getConversationInfoFunc = func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
		return nil, err
	}
	return b
}

// WithGetConversationHistory configures the conversation history returned by the mock.
func (b *MockSlackAPIBuilder) WithGetConversationHistory(messages []slack.Message) *MockSlackAPIBuilder {
	b.mock.getConversationHistoryFunc = func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{
			Messages: messages,
		}, nil
	}
	return b
}

// WithGetConversationHistoryError configures the mock to fail when getting conversation history.
func (b *MockSlackAPIBuilder) WithGetConversationHistoryError(err error) *MockSlackAPIBuilder {
	b.mock.getConversationHistoryFunc = func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
		return nil, err
	}
	return b
}

// WithGetUserInfo configures the user info returned by the mock.
func (b *MockSlackAPIBuilder) WithGetUserInfo(user *slack.User) *MockSlackAPIBuilder {
	b.mock.getUserInfoFunc = func(ctx context.Context, userID string) (*slack.User, error) {
		return user, nil
	}
	return b
}

// WithGetUserInfoError configures the mock to fail when getting user info.
func (b *MockSlackAPIBuilder) WithGetUserInfoError(err error) *MockSlackAPIBuilder {
	b.mock.getUserInfoFunc = func(ctx context.Context, userID string) (*slack.User, error) {
		return nil, err
	}
	return b
}

// WithGetUserPresence configures the user presence returned by the mock.
func (b *MockSlackAPIBuilder) WithGetUserPresence(presence string) *MockSlackAPIBuilder {
	b.mock.getUserPresenceFunc = func(ctx context.Context, userID string) (*slack.UserPresence, error) {
		return &slack.UserPresence{
			Presence: presence,
		}, nil
	}
	return b
}

// WithGetUserPresenceError configures the mock to fail when getting user presence.
func (b *MockSlackAPIBuilder) WithGetUserPresenceError(err error) *MockSlackAPIBuilder {
	b.mock.getUserPresenceFunc = func(ctx context.Context, userID string) (*slack.UserPresence, error) {
		return nil, err
	}
	return b
}

// WithOpenConversation configures the conversation returned when opening a DM.
func (b *MockSlackAPIBuilder) WithOpenConversation(channel *slack.Channel) *MockSlackAPIBuilder {
	b.mock.openConversationFunc = func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
		return channel, false, false, nil
	}
	return b
}

// WithOpenConversationError configures the mock to fail when opening conversations.
func (b *MockSlackAPIBuilder) WithOpenConversationError(err error) *MockSlackAPIBuilder {
	b.mock.openConversationFunc = func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
		return nil, false, false, err
	}
	return b
}

// WithSearchMessages configures the search results returned by the mock.
func (b *MockSlackAPIBuilder) WithSearchMessages(messages *slack.SearchMessages) *MockSlackAPIBuilder {
	b.mock.searchMessagesFunc = func(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error) {
		return messages, nil
	}
	return b
}

// WithSearchMessagesError configures the mock to fail when searching messages.
func (b *MockSlackAPIBuilder) WithSearchMessagesError(err error) *MockSlackAPIBuilder {
	b.mock.searchMessagesFunc = func(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error) {
		return nil, err
	}
	return b
}

// WithGetUsersInConversation configures the users in a conversation.
func (b *MockSlackAPIBuilder) WithGetUsersInConversation(users []string) *MockSlackAPIBuilder {
	b.mock.getUsersInConversationFunc = func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
		return users, "", nil
	}
	return b
}

// WithGetUsersInConversationError configures the mock to fail when getting users in conversation.
func (b *MockSlackAPIBuilder) WithGetUsersInConversationError(err error) *MockSlackAPIBuilder {
	b.mock.getUsersInConversationFunc = func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
		return nil, "", err
	}
	return b
}

// WithGetConversations configures the conversations returned by the mock.
func (b *MockSlackAPIBuilder) WithGetConversations(channels []slack.Channel) *MockSlackAPIBuilder {
	b.mock.getConversationsFunc = func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
		return channels, "", nil
	}
	return b
}

// WithGetConversationsError configures the mock to fail when getting conversations.
func (b *MockSlackAPIBuilder) WithGetConversationsError(err error) *MockSlackAPIBuilder {
	b.mock.getConversationsFunc = func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
		return nil, "", err
	}
	return b
}

// Build returns the configured mockSlackAPI.
func (b *MockSlackAPIBuilder) Build() *mockSlackAPI {
	return b.mock
}

// Common error types for testing
var (
	ErrAPIError         = errors.New("api error")
	ErrNotFound         = errors.New("not found")
	ErrPermissionDenied = errors.New("permission_denied")
	ErrRateLimited      = errors.New("rate_limited")
)
