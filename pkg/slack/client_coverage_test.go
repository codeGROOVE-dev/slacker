package slack

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/slack-go/slack"
)

// TestPostThreadReply_ErrorCases tests error handling in PostThreadReply.
func TestPostThreadReply_ErrorCases(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("channel_not_found", func(t *testing.T) {
		api := &mockAPI{
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				return "", "", errors.New("channel_not_found")
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		err := client.PostThreadReply(ctx, "C123", "123.456", "Test")
		if err == nil {
			t.Fatal("expected error for channel_not_found")
		}
	})

	t.Run("not_in_channel", func(t *testing.T) {
		api := &mockAPI{
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				return "", "", errors.New("not_in_channel")
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		err := client.PostThreadReply(ctx, "C123", "123.456", "Test")
		if err == nil {
			t.Fatal("expected error for not_in_channel")
		}
	})

	t.Run("thread_not_found", func(t *testing.T) {
		api := &mockAPI{
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				return "", "", errors.New("thread_not_found")
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		err := client.PostThreadReply(ctx, "C123", "123.456", "Test")
		if err == nil {
			t.Fatal("expected error for thread_not_found")
		}
	})

	t.Run("rate_limit_retry", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				callCount++
				if callCount == 1 {
					return "", "", &slack.RateLimitedError{RetryAfter: 1 * time.Millisecond}
				}
				return "C123", "123.457", nil
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		err := client.PostThreadReply(ctx, "C123", "123.456", "Test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls (1 retry), got %d", callCount)
		}
	})

	t.Run("retryable_error", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				callCount++
				if callCount < 3 {
					return "", "", errors.New("temporary error")
				}
				return "C123", "123.457", nil
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		err := client.PostThreadReply(ctx, "C123", "123.456", "Test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 3 {
			t.Errorf("expected 3 calls, got %d", callCount)
		}
	})
}

// TestHasRecentDMAboutPR_WithStateStore tests HasRecentDMAboutPR with a state store.
func TestHasRecentDMAboutPR_WithStateStore(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	prURL := "https://github.com/test/repo/pull/123"

	t.Run("with_recent_dm", func(t *testing.T) {
		mockStore := &programmableMockStateStore{
			dmMessages: map[string]state.DMInfo{
				"U001:" + prURL: {
					ChannelID:   "D123",
					MessageTS:   "123.456",
					MessageText: "Old message",
					SentAt:      time.Now().Add(-30 * time.Minute), // 30 mins ago
				},
			},
		}

		api := &mockAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "D123"}}}, false, false, nil
			},
			authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
				return &slack.AuthTestResponse{UserID: "UBOT"}, nil
			},
			getConversationHistoryFunc: func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
				return &slack.GetConversationHistoryResponse{
					Messages: []slack.Message{
						{
							Msg: slack.Msg{
								User:      "UBOT",
								Timestamp: "123.456",
								Text:      "Message with " + prURL,
							},
						},
					},
				}, nil
			},
		}

		client := &Client{
			api:        api,
			stateStore: mockStore,
			cache:      &apiCache{entries: make(map[string]cacheEntry)},
		}

		hasRecent, err := client.HasRecentDMAboutPR(ctx, "U001", prURL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasRecent {
			t.Error("expected recent DM to be found")
		}
	})

	t.Run("open_conversation_error", func(t *testing.T) {
		api := &mockAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return nil, false, false, errors.New("api error")
			},
		}

		client := &Client{
			api: api,
		}

		_, err := client.HasRecentDMAboutPR(ctx, "U001", prURL)
		if err == nil {
			t.Fatal("expected error from OpenConversation")
		}
	})

	t.Run("bot_info_error", func(t *testing.T) {
		api := &mockAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "D123"}}}, false, false, nil
			},
			authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
				return nil, errors.New("auth error")
			},
		}

		client := &Client{
			api:   api,
			cache: &apiCache{entries: make(map[string]cacheEntry)},
		}

		_, err := client.HasRecentDMAboutPR(ctx, "U001", prURL)
		if err == nil {
			t.Fatal("expected error from BotInfo")
		}
	})

	t.Run("conversation_history_error", func(t *testing.T) {
		api := &mockAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "D123"}}}, false, false, nil
			},
			authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
				return &slack.AuthTestResponse{UserID: "UBOT"}, nil
			},
			getConversationHistoryFunc: func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
				return nil, errors.New("history error")
			},
		}

		client := &Client{
			api:   api,
			cache: &apiCache{entries: make(map[string]cacheEntry)},
		}

		// When history check fails, function errs on side of sending (returns false, nil)
		hasRecent, err := client.HasRecentDMAboutPR(ctx, "U001", prURL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasRecent {
			t.Error("expected false when history check fails (err on side of sending)")
		}
	})
}

// TestSendDirectMessage_Errors tests error handling in SendDirectMessage.
func TestSendDirectMessage_Errors(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("open_conversation_fails", func(t *testing.T) {
		api := &mockAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return nil, false, false, errors.New("api error")
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		_, _, err := client.SendDirectMessage(ctx, "U001", "Test message")
		if err == nil {
			t.Fatal("expected error from OpenConversation")
		}
	})

	t.Run("post_message_fails", func(t *testing.T) {
		api := &mockAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "D123"}}}, false, false, nil
			},
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				return "", "", errors.New("post error")
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		_, _, err := client.SendDirectMessage(ctx, "U001", "Test message")
		if err == nil {
			t.Fatal("expected error from PostMessage")
		}
	})

	t.Run("rate_limit_during_send", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "D123"}}}, false, false, nil
			},
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				callCount++
				if callCount == 1 {
					return "", "", &slack.RateLimitedError{RetryAfter: 1 * time.Millisecond}
				}
				return "D123", "123.456", nil
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		channelID, msgTS, err := client.SendDirectMessage(ctx, "U001", "Test message")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if channelID != "D123" {
			t.Errorf("expected channel D123, got %s", channelID)
		}
		if msgTS != "123.456" {
			t.Errorf("expected timestamp 123.456, got %s", msgTS)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls (1 retry), got %d", callCount)
		}
	})
	//nolint:tparallel // Tests share resources, cannot run subtests in parallel
}

// TestSaveDMMessageInfo_WithStore tests SaveDMMessageInfo with a state store.
func TestSaveDMMessageInfo_WithStore(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("saves_to_store", func(t *testing.T) {
		mockStore := &programmableMockStateStore{
			dmMessages: make(map[string]state.DMInfo),
		}

		client := &Client{
			stateStore: mockStore,
		}

		prURL := "https://github.com/test/repo/pull/123"
		err := client.SaveDMMessageInfo(ctx, "U001", prURL, "D123", "123.456", "Test message")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify it was saved
		info, ok := mockStore.dmMessages["U001:"+prURL]
		if !ok {
			t.Fatal("DM info was not saved")
		}
		if info.ChannelID != "D123" {
			t.Errorf("expected channel D123, got %s", info.ChannelID)
		}
		if info.MessageTS != "123.456" {
			t.Errorf("expected timestamp 123.456, got %s", info.MessageTS)
		}
		if info.MessageText != "Test message" {
			t.Errorf("expected text 'Test message', got %s", info.MessageText)
		}
	})

	t.Run("store_save_error", func(t *testing.T) {
		mockStore := &programmableMockStateStore{
			dmMessages:       make(map[string]state.DMInfo),
			saveDMMessageErr: errors.New("storage error"),
		}

		client := &Client{
			stateStore: mockStore,
		}

		prURL := "https://github.com/test/repo/pull/123"
		err := client.SaveDMMessageInfo(ctx, "U001", prURL, "D123", "123.456", "Test message")
		if err == nil {
			t.Fatal("expected error from state store")
		}
		//nolint:tparallel // Tests share resources, cannot run subtests in parallel
	})
}

// TestPostThread_Errors tests error handling in PostThread.
func TestPostThread_Errors(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("channel_not_found_during_check", func(t *testing.T) {
		api := &mockAPI{
			getConversationInfoFunc: func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
				return nil, errors.New("channel_not_found")
			},
			getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
				return nil, "", errors.New("not_in_channel")
			},
		}

		client := &Client{
			api:        api,
			cache:      &apiCache{entries: make(map[string]cacheEntry)},
			retryDelay: 1 * time.Millisecond,
		}

		_, err := client.PostThread(ctx, "C999", "Test", nil)
		if err == nil {
			t.Fatal("expected error for nonexistent channel")
		}
		if !contains(err.Error(), "does not exist") {
			t.Errorf("expected 'does not exist' error, got: %v", err)
		}
	})

	t.Run("bot_not_in_channel", func(t *testing.T) {
		api := &mockAPI{
			getConversationInfoFunc: func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
				return &slack.Channel{}, nil
			},
			getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
				return []string{"U001"}, "", nil // Bot not in list
			},
			authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
				return &slack.AuthTestResponse{UserID: "UBOT"}, nil
			},
		}

		client := &Client{
			api:        api,
			cache:      &apiCache{entries: make(map[string]cacheEntry)},
			retryDelay: 1 * time.Millisecond,
		}

		_, err := client.PostThread(ctx, "C123", "Test", nil)
		if err == nil {
			t.Fatal("expected error for bot not in channel")
		}
		if !contains(err.Error(), "not a member") {
			t.Errorf("expected 'not a member' error, got: %v", err)
		}
	})

	t.Run("post_with_not_in_channel_error", func(t *testing.T) {
		api := &mockAPI{
			getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
				return []string{"UBOT"}, "", nil
			},
			authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
				return &slack.AuthTestResponse{UserID: "UBOT"}, nil
			},
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				return "", "", errors.New("not_in_channel")
			},
		}

		client := &Client{
			api:        api,
			cache:      &apiCache{entries: make(map[string]cacheEntry)},
			retryDelay: 1 * time.Millisecond,
		}

		_, err := client.PostThread(ctx, "C123", "Test", nil)
		if err == nil {
			t.Fatal("expected error for not_in_channel")
		}
	})

	t.Run("post_with_rate_limit", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			getUsersInConversationFunc: func(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
				return []string{"UBOT"}, "", nil
			},
			authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
				return &slack.AuthTestResponse{UserID: "UBOT"}, nil
			},
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				callCount++
				if callCount == 1 {
					return "", "", &slack.RateLimitedError{RetryAfter: 1 * time.Millisecond}
				}
				return "C123", "123.456", nil
			},
		}

		client := &Client{
			api:        api,
			cache:      &apiCache{entries: make(map[string]cacheEntry)},
			retryDelay: 1 * time.Millisecond,
		}

		ts, err := client.PostThread(ctx, "C123", "Test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ts != "123.456" {
			t.Errorf("expected timestamp 123.456, got %s", ts)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls (1 retry), got %d", callCount)
			//nolint:tparallel // Tests share resources, cannot run subtests in parallel
		}
	})
}

// TestUpdateMessage_EdgeCases tests edge cases in UpdateMessage.
func TestUpdateMessage_EdgeCases(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("message_not_found", func(t *testing.T) {
		api := &mockAPI{
			updateMessageFunc: func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
				return "", "", "", errors.New("message_not_found")
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		err := client.UpdateMessage(ctx, "C123", "123.456", "New text")
		if err == nil {
			t.Fatal("expected error for message_not_found")
		}
	})

	t.Run("rate_limit_on_update", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			updateMessageFunc: func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
				callCount++
				if callCount == 1 {
					return "", "", "", &slack.RateLimitedError{RetryAfter: 1 * time.Millisecond}
				}
				return "C123", "123.456", "New text", nil
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		err := client.UpdateMessage(ctx, "C123", "123.456", "New text")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 2 {
			t.Errorf("expected 2 calls (1 retry), got %d", callCount)
		}
	})
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
