package slack

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// TestSendDirectMessageWithBlocks tests sending DM with blocks.
func TestSendDirectMessageWithBlocks(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123456",
					},
				},
			}, true, true, nil
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

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "Test message", false, false),
			nil, nil,
		),
	}

	channelID, messageTS, err := client.SendDirectMessageWithBlocks(context.Background(), "U123", blocks)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if channelID != "D123456" {
		t.Errorf("Expected channel ID D123456, got %s", channelID)
	}

	if messageTS == "" {
		t.Error("Expected non-empty message timestamp")
	}
}

// TestSendDirectMessageWithBlocks_OpenConversationError tests error opening conversation.
func TestSendDirectMessageWithBlocks_OpenConversationError(t *testing.T) {
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
			slack.NewTextBlockObject("mrkdwn", "Test", false, false),
			nil, nil,
		),
	}

	_, _, err := client.SendDirectMessageWithBlocks(context.Background(), "U123", blocks)

	if err == nil {
		t.Error("Expected error when opening conversation fails")
	}
}

// TestSendDirectMessageWithBlocks_PostMessageError tests error posting message.
func TestSendDirectMessageWithBlocks_PostMessageError(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123456",
					},
				},
			}, true, true, nil
		},
		postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
			return "", "", errors.New("failed to post message")
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
			slack.NewTextBlockObject("mrkdwn", "Test", false, false),
			nil, nil,
		),
	}

	_, _, err := client.SendDirectMessageWithBlocks(context.Background(), "U123", blocks)

	if err == nil {
		t.Error("Expected error when posting message fails")
	}
}

// TestFindDMMessagesInHistory tests finding DM messages in history.
func TestFindDMMessagesInHistory(t *testing.T) {
	t.Parallel()

	botUserID := "UBOT123"
	prURL := "https://github.com/org/repo/pull/123"

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123456",
					},
				},
			}, true, true, nil
		},
		getConversationHistoryFunc: func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							User:      botUserID,
							Text:      "Check out " + prURL,
							Timestamp: "1234567890.123456",
						},
					},
					{
						Msg: slack.Msg{
							User:      "UOTHER",
							Text:      "Some other message",
							Timestamp: "1234567891.123456",
						},
					},
				},
				HasMore: false,
			}, nil
		},
	}

	client := &Client{
		api:    mockAPI,
		teamID: "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Set bot info in cache
	client.cache.set("bot_auth_test", &slack.AuthTestResponse{
		UserID: botUserID,
	}, time.Hour)

	locations, err := client.FindDMMessagesInHistory(
		context.Background(),
		"U123",
		prURL,
		time.Now().Add(-24*time.Hour),
	)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(locations) != 1 {
		t.Errorf("Expected 1 location, got %d", len(locations))
	}

	if len(locations) > 0 {
		if locations[0].ChannelID != "D123456" {
			t.Errorf("Expected channel ID D123456, got %s", locations[0].ChannelID)
		}
		if locations[0].MessageTS != "1234567890.123456" {
			t.Errorf("Expected timestamp 1234567890.123456, got %s", locations[0].MessageTS)
		}
	}
}

// TestFindDMMessagesInHistory_OpenConversationError tests error opening conversation.
func TestFindDMMessagesInHistory_OpenConversationError(t *testing.T) {
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

	_, err := client.FindDMMessagesInHistory(
		context.Background(),
		"U123",
		"https://github.com/org/repo/pull/123",
		time.Now().Add(-24*time.Hour),
	)

	if err == nil {
		t.Error("Expected error when opening conversation fails")
	}
}

// TestFindDMMessagesInHistory_BotInfoError tests error getting bot info.
func TestFindDMMessagesInHistory_BotInfoError(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123456",
					},
				},
			}, true, true, nil
		},
		authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return nil, errors.New("auth test failed")
		},
	}

	client := &Client{
		api:    mockAPI,
		teamID: "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	_, err := client.FindDMMessagesInHistory(
		context.Background(),
		"U123",
		"https://github.com/org/repo/pull/123",
		time.Now().Add(-24*time.Hour),
	)

	if err == nil {
		t.Error("Expected error when getting bot info fails")
	}
}

// TestFindDMMessagesInHistory_GetHistoryError tests error getting conversation history.
func TestFindDMMessagesInHistory_GetHistoryError(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123456",
					},
				},
			}, true, true, nil
		},
		getConversationHistoryFunc: func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
			return nil, errors.New("failed to get history")
		},
	}

	client := &Client{
		api:    mockAPI,
		teamID: "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Set bot info in cache
	client.cache.set("bot_info", &slack.AuthTestResponse{
		UserID: "UBOT123",
	}, time.Hour)

	_, err := client.FindDMMessagesInHistory(
		context.Background(),
		"U123",
		"https://github.com/org/repo/pull/123",
		time.Now().Add(-24*time.Hour),
	)

	if err == nil {
		t.Error("Expected error when getting history fails")
	}
}

// TestFindDMMessagesInHistory_MultiplePagesNoPRURL tests pagination without finding PR URL.
func TestFindDMMessagesInHistory_MultiplePagesNoPRURL(t *testing.T) {
	t.Parallel()

	botUserID := "UBOT123"
	callCount := 0

	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123456",
					},
				},
			}, true, true, nil
		},
		getConversationHistoryFunc: func(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
			callCount++
			hasMore := callCount < 3 // Return 3 pages

			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							User:      botUserID,
							Text:      "Some other message",
							Timestamp: "1234567890.123456",
						},
					},
				},
				HasMore: hasMore,
			}, nil
		},
	}

	client := &Client{
		api:    mockAPI,
		teamID: "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Set bot info in cache
	client.cache.set("bot_auth_test", &slack.AuthTestResponse{
		UserID: botUserID,
	}, time.Hour)

	locations, err := client.FindDMMessagesInHistory(
		context.Background(),
		"U123",
		"https://github.com/org/repo/pull/999",
		time.Now().Add(-24*time.Hour),
	)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(locations) != 0 {
		t.Errorf("Expected 0 locations, got %d", len(locations))
	}

	if callCount != 3 {
		t.Errorf("Expected 3 API calls, got %d", callCount)
	}
}
