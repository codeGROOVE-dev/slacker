package slack

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// testTime returns a fixed time for testing
func testTime() time.Time {
	return time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
}

// TestUserInfo_userNotFound tests the user_not_found error path
func TestUserInfo_userNotFound(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return nil, errors.New("user_not_found")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	got, err := client.UserInfo(context.Background(), "U999")

	if err == nil {
		t.Error("UserInfo(U999) = nil error, want error for user_not_found")
	}

	if got != nil {
		t.Errorf("UserInfo(U999) = %v, want nil for user_not_found", got)
	}

	if !strings.Contains(err.Error(), "failed to get user info") {
		t.Errorf("UserInfo(U999) error = %v, want error containing 'failed to get user info'", err)
	}
}

// TestUserPresence_userNotFound tests the user_not_found error path for presence
func TestUserPresence_userNotFound(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
			return nil, errors.New("user_not_found")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	got, err := client.UserPresence(context.Background(), "U999")

	if err == nil {
		t.Error("UserPresence(U999) = nil error, want error for user_not_found")
	}

	if got != "" {
		t.Errorf("UserPresence(U999) = %q, want empty string for user_not_found", got)
	}
}

// TestWorkspaceInfo_error tests error handling in WorkspaceInfo
func TestWorkspaceInfo_error(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
			return nil, errors.New("api error")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	got, err := client.WorkspaceInfo(context.Background())

	if err == nil {
		t.Error("WorkspaceInfo() = nil error, want error when API fails")
	}

	if got != nil {
		t.Errorf("WorkspaceInfo() = %v, want nil when API fails", got)
	}
}

// TestPostThread_emptyChannel tests PostThread with empty channel name
func TestPostThread_emptyChannel(t *testing.T) {
	t.Parallel()

	client := &Client{
		api: &mockAPI{},
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	got, err := client.PostThread(context.Background(), "", "test message", nil)

	if err == nil {
		t.Error("PostThread(\"\", \"test message\", nil) = nil error, want error for empty channel")
	}

	if got != "" {
		t.Errorf("PostThread(\"\", \"test message\", nil) = %q, want empty string on error", got)
	}
}

// TestUpdateMessage_error tests UpdateMessage error path
func TestUpdateMessage_error(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		updateMessageFunc: func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
			return "", "", "", errors.New("update failed")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	err := client.UpdateMessage(context.Background(), "C123", "123.456", "new text")

	if err == nil {
		t.Error("UpdateMessage(C123, 123.456, \"new text\") = nil, want error when update fails")
	}
}

// TestPostThreadReply_error tests PostThreadReply error path
func TestPostThreadReply_error(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
			return "", "", errors.New("post failed")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	err := client.PostThreadReply(context.Background(), "C123", "123.456", "reply text")

	if err == nil {
		t.Error("PostThreadReply(C123, 123.456, \"reply text\") = nil, want error when post fails")
	}
}

// TestSendDirectMessage_openConversationError tests error when opening DM fails
func TestSendDirectMessage_openConversationError(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return nil, false, false, errors.New("failed to open conversation")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	gotChannel, gotTS, err := client.SendDirectMessage(context.Background(), "U123", "test message")

	if err == nil {
		t.Error("SendDirectMessage(U123, \"test message\") = nil error, want error when open conversation fails")
	}

	if gotChannel != "" {
		t.Errorf("SendDirectMessage(U123, \"test message\") channel = %q, want empty on error", gotChannel)
	}

	if gotTS != "" {
		t.Errorf("SendDirectMessage(U123, \"test message\") timestamp = %q, want empty on error", gotTS)
	}
}

// TestSendDirectMessageWithBlocks_openConversationError tests error path for blocks
func TestSendDirectMessageWithBlocks_openConversationError(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return nil, false, false, errors.New("failed to open conversation")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "test", false, false),
			nil, nil,
		),
	}

	gotChannel, gotTS, err := client.SendDirectMessageWithBlocks(context.Background(), "U123", blocks)

	if err == nil {
		t.Error("SendDirectMessageWithBlocks(U123, blocks) = nil error, want error when open conversation fails")
	}

	if gotChannel != "" {
		t.Errorf("SendDirectMessageWithBlocks(U123, blocks) channel = %q, want empty on error", gotChannel)
	}

	if gotTS != "" {
		t.Errorf("SendDirectMessageWithBlocks(U123, blocks) timestamp = %q, want empty on error", gotTS)
	}
}

// TestFindDMMessagesInHistory_openConversationError tests error when opening DM fails
func TestFindDMMessagesInHistory_openConversationError(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return nil, false, false, errors.New("failed to open conversation")
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	got, err := client.FindDMMessagesInHistory(context.Background(), "U123", "test-pr-url", testTime())

	if err == nil {
		t.Error("FindDMMessagesInHistory(U123, \"test-pr-url\", time) = nil error, want error when open conversation fails")
	}

	if len(got) != 0 {
		t.Errorf("FindDMMessagesInHistory(U123, \"test-pr-url\", time) = %d results, want 0 on error", len(got))
	}
}

// TestUpdateMessage_success tests successful message update
func TestUpdateMessage_success(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		updateMessageFunc: func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
			return channelID, timestamp, "updated text", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	err := client.UpdateMessage(context.Background(), "C123", "1234567890.123456", "Updated text")

	if err != nil {
		t.Errorf("UpdateMessage(C123, 1234567890.123456) = %v, want nil", err)
	}
}

// TestPostThreadReply_success tests successful thread reply
func TestPostThreadReply_success(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
			return channelID, "1234567890.123457", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	err := client.PostThreadReply(context.Background(), "C123", "1234567890.123456", "Reply text")

	if err != nil {
		t.Errorf("PostThreadReply(C123, 1234567890.123456) = %v, want nil", err)
	}
}

// TestUserInfo_genericErrorWithRetries tests retry logic for temporary errors
func TestUserInfo_genericErrorWithRetries(t *testing.T) {
	t.Parallel()

	callCount := 0
	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			callCount++
			if callCount < 3 {
				return nil, errors.New("temporary error")
			}
			return &slack.User{ID: userID}, nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	user, err := client.UserInfo(context.Background(), "U123")

	if err != nil {
		t.Errorf("UserInfo(U123) after retries = %v, want nil", err)
	}

	if user == nil || user.ID != "U123" {
		t.Errorf("UserInfo(U123) returned user = %v, want user with ID U123", user)
	}

	if callCount < 3 {
		t.Errorf("UserInfo(U123) made %d calls, want at least 3 (with retries)", callCount)
	}
}

// TestUserPresence_genericErrorWithRetries tests retry logic for presence
func TestUserPresence_genericErrorWithRetries(t *testing.T) {
	t.Parallel()

	callCount := 0
	mockAPI := &mockAPI{
		getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
			callCount++
			if callCount < 2 {
				return nil, errors.New("temporary error")
			}
			return &slack.UserPresence{Presence: "active"}, nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	presence, err := client.UserPresence(context.Background(), "U123")

	if err != nil {
		t.Errorf("UserPresence(U123) after retries = %v, want nil", err)
	}

	if presence != "active" {
		t.Errorf("UserPresence(U123) = %q, want \"active\"", presence)
	}

	if callCount < 2 {
		t.Errorf("UserPresence(U123) made %d calls, want at least 2 (with retries)", callCount)
	}
}

// TestIsUserInChannel_success tests checking if user is in channel
func TestIsUserInChannel_success(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
			return []string{"U123", "U456", "U789"}, "", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	isIn := client.IsUserInChannel(context.Background(), "C123", "U456")

	if !isIn {
		t.Error("IsUserInChannel(C123, U456) = false, want true")
	}
}

// TestIsUserInChannel_notInChannel tests user not in channel
func TestIsUserInChannel_notInChannel(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
			return []string{"U123", "U789"}, "", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	isIn := client.IsUserInChannel(context.Background(), "C123", "U456")

	if isIn {
		t.Error("IsUserInChannel(C123, U456) = true, want false")
	}
}

// TestIsBotInChannel_success tests checking if bot is in channel
func TestIsBotInChannel_success(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "UBOT123"}, nil
		},
		getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
			return []string{"U123", "UBOT123", "U456"}, "", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	isIn := client.IsBotInChannel(context.Background(), "C123")

	if !isIn {
		t.Error("IsBotInChannel(C123) = false, want true")
	}
}

// TestIsBotInChannel_notInChannel tests bot not in channel
func TestIsBotInChannel_notInChannel(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "UBOT123"}, nil
		},
		getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
			return []string{"U123", "U456"}, "", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	isIn := client.IsBotInChannel(context.Background(), "C123")

	if isIn {
		t.Error("IsBotInChannel(C123) = true, want false")
	}
}

// TestPostThread_success tests successful thread creation
func TestPostThread_success(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "UBOT123"}, nil
		},
		getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
			return []string{"U123", "UBOT123"}, "", nil
		},
		postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
			return channelID, "1234567890.123456", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	ts, err := client.PostThread(context.Background(), "C123", "Thread message", nil)

	if err != nil {
		t.Errorf("PostThread(C123, ...) = %v, want nil", err)
	}

	if ts != "1234567890.123456" {
		t.Errorf("PostThread(C123, ...) = %q, want %q", ts, "1234567890.123456")
	}
}

// TestSendDirectMessage_success tests successful DM sending
func TestSendDirectMessage_success(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{ID: "D123"},
				},
			}, false, false, nil
		},
		postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
			return channelID, "1234567890.123456", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	channel, ts, err := client.SendDirectMessage(context.Background(), "U123", "DM message")

	if err != nil {
		t.Errorf("SendDirectMessage(U123, ...) = %v, want nil", err)
	}

	if channel != "D123" {
		t.Errorf("SendDirectMessage(U123, ...) channel = %q, want %q", channel, "D123")
	}

	if ts != "1234567890.123456" {
		t.Errorf("SendDirectMessage(U123, ...) ts = %q, want %q", ts, "1234567890.123456")
	}
}
