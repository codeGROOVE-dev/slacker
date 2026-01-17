package slack

import (
	"context"
	"testing"

	"github.com/slack-go/slack"
)

// TestSendDirectMessage_rateLimitError tests rate limiting in SendDirectMessage
func TestSendDirectMessage_rateLimitError(t *testing.T) {
	t.Parallel()

	callCount := 0
	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123",
					},
				},
			}, true, true, nil
		},
		postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
			callCount++
			if callCount < 3 {
				// Return rate limit error first couple times
				return "", "", slack.StatusCodeError{
					Code:   429,
					Status: "Too Many Requests",
				}
			}
			// Then succeed
			return channelID, "123.456", nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	gotChannel, gotTS, err := client.SendDirectMessage(context.Background(), "U123", "test message")

	if err != nil {
		t.Errorf("SendDirectMessage(U123, \"test message\") = error %v, want nil after retries", err)
	}

	if gotChannel != "D123" {
		t.Errorf("SendDirectMessage(U123, \"test message\") channel = %q, want D123", gotChannel)
	}

	if gotTS != "123.456" {
		t.Errorf("SendDirectMessage(U123, \"test message\") timestamp = %q, want 123.456", gotTS)
	}

	if callCount < 3 {
		t.Errorf("SendDirectMessage(U123, \"test message\") made %d calls, want at least 3 (with retries)", callCount)
	}
}

// TestSendDirectMessageWithBlocks_rateLimitError tests rate limiting with blocks
func TestSendDirectMessageWithBlocks_rateLimitError(t *testing.T) {
	t.Parallel()

	callCount := 0
	mockAPI := &mockAPI{
		openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
			return &slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "D123",
					},
				},
			}, true, true, nil
		},
		postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
			callCount++
			if callCount < 2 {
				// Return rate limit error first time
				return "", "", slack.StatusCodeError{
					Code:   429,
					Status: "Too Many Requests",
				}
			}
			// Then succeed
			return channelID, "123.456", nil
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

	if err != nil {
		t.Errorf("SendDirectMessageWithBlocks(U123, blocks) = error %v, want nil after retries", err)
	}

	if gotChannel != "D123" {
		t.Errorf("SendDirectMessageWithBlocks(U123, blocks) channel = %q, want D123", gotChannel)
	}

	if gotTS != "123.456" {
		t.Errorf("SendDirectMessageWithBlocks(U123, blocks) timestamp = %q, want 123.456", gotTS)
	}

	if callCount < 2 {
		t.Errorf("SendDirectMessageWithBlocks(U123, blocks) made %d calls, want at least 2 (with retries)", callCount)
	}
}

// Rate limit tests for SendDirectMessage and SendDirectMessageWithBlocks above
