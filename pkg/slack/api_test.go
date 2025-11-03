package slack

import (
	"context"
	"errors"
	"testing"

	"github.com/slack-go/slack"
)

func TestSlackAPIWrapper(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("RawClient", func(t *testing.T) {
		rawClient := slack.New("test-token")
		wrapper := newSlackAPIWrapper(rawClient).(*slackAPIWrapper)

		if wrapper.RawClient() != rawClient {
			t.Error("expected RawClient to return the wrapped client")
		}
	})

	t.Run("GetTeamInfoContext", func(t *testing.T) {
		expectedInfo := &slack.TeamInfo{
			ID:   "T123",
			Name: "Test Team",
		}

		api := &mockSlackAPI{
			getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
				return expectedInfo, nil
			},
		}

		info, err := api.GetTeamInfoContext(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.ID != expectedInfo.ID {
			t.Errorf("expected ID %s, got %s", expectedInfo.ID, info.ID)
		}
	})

	t.Run("AuthTestContext", func(t *testing.T) {
		expectedResp := &slack.AuthTestResponse{
			UserID: "U123",
			TeamID: "T123",
		}

		api := &mockSlackAPI{
			authTestFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
				return expectedResp, nil
			},
		}

		resp, err := api.AuthTestContext(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.UserID != expectedResp.UserID {
			t.Errorf("expected UserID %s, got %s", expectedResp.UserID, resp.UserID)
		}
	})

	t.Run("GetConversationInfoContext", func(t *testing.T) {
		expectedChan := &slack.Channel{
			GroupConversation: slack.GroupConversation{
				Conversation: slack.Conversation{
					ID: "C123",
				},
				Name: "test-channel",
			},
		}

		api := &mockSlackAPI{
			getConversationInfoFunc: func(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error) {
				return expectedChan, nil
			},
		}

		input := &slack.GetConversationInfoInput{ChannelID: "C123"}
		ch, err := api.GetConversationInfoContext(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if ch.ID != expectedChan.ID {
			t.Errorf("expected ID %s, got %s", expectedChan.ID, ch.ID)
		}
	})

	t.Run("OpenConversationContext", func(t *testing.T) {
		expectedChan := &slack.Channel{
			GroupConversation: slack.GroupConversation{
				Conversation: slack.Conversation{
					ID: "D123",
				},
			},
		}

		api := &mockSlackAPI{
			openConversationFunc: func(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
				return expectedChan, false, false, nil
			},
		}

		params := &slack.OpenConversationParameters{
			Users: []string{"U123"},
		}
		ch, _, _, err := api.OpenConversationContext(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if ch.ID != expectedChan.ID {
			t.Errorf("expected ID %s, got %s", expectedChan.ID, ch.ID)
		}
	})

	t.Run("PostMessageContext", func(t *testing.T) {
		api := &mockSlackAPI{
			postMessageFunc: func(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
				return "C123", "1234567890.123456", nil
			},
		}

		channel, ts, err := api.PostMessageContext(ctx, "C123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if channel != "C123" {
			t.Errorf("expected channel C123, got %s", channel)
		}

		if ts == "" {
			t.Error("expected non-empty timestamp")
		}
	})

	t.Run("UpdateMessageContext", func(t *testing.T) {
		api := &mockSlackAPI{
			updateMessageFunc: func(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
				return channelID, timestamp, "Updated text", nil
			},
		}

		channel, ts, text, err := api.UpdateMessageContext(ctx, "C123", "1234567890.123456")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if channel != "C123" {
			t.Errorf("expected channel C123, got %s", channel)
		}

		if ts != "1234567890.123456" {
			t.Errorf("expected timestamp 1234567890.123456, got %s", ts)
		}

		if text != "Updated text" {
			t.Errorf("expected text 'Updated text', got %s", text)
		}
	})

	t.Run("GetUserInfoContext", func(t *testing.T) {
		expectedUser := &slack.User{
			ID:   "U123",
			Name: "testuser",
		}

		api := &mockSlackAPI{
			getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
				return expectedUser, nil
			},
		}

		user, err := api.GetUserInfoContext(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if user.ID != expectedUser.ID {
			t.Errorf("expected ID %s, got %s", expectedUser.ID, user.ID)
		}
	})

	t.Run("GetUserPresenceContext", func(t *testing.T) {
		expectedPresence := &slack.UserPresence{
			Presence: "active",
		}

		api := &mockSlackAPI{
			getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
				return expectedPresence, nil
			},
		}

		presence, err := api.GetUserPresenceContext(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if presence.Presence != "active" {
			t.Errorf("expected presence active, got %s", presence.Presence)
		}
	})

	t.Run("AddReactionContext", func(t *testing.T) {
		api := &mockSlackAPI{
			addReactionFunc: func(ctx context.Context, name string, item slack.ItemRef) error {
				if name != "thumbsup" {
					return errors.New("unexpected reaction name")
				}
				return nil
			},
		}

		item := slack.ItemRef{
			Channel:   "C123",
			Timestamp: "1234567890.123456",
		}

		err := api.AddReactionContext(ctx, "thumbsup", item)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("RemoveReactionContext", func(t *testing.T) {
		api := &mockSlackAPI{
			removeReactionFunc: func(ctx context.Context, name string, item slack.ItemRef) error {
				if name != "thumbsup" {
					return errors.New("unexpected reaction name")
				}
				return nil
			},
		}

		item := slack.ItemRef{
			Channel:   "C123",
			Timestamp: "1234567890.123456",
		}

		err := api.RemoveReactionContext(ctx, "thumbsup", item)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("SearchMessagesContext", func(t *testing.T) {
		expectedResults := &slack.SearchMessages{
			Matches: []slack.SearchMessage{
				{
					Timestamp: "1234567890.123456",
					Text:      "test message",
				},
			},
		}

		api := &mockSlackAPI{
			searchMessagesFunc: func(ctx context.Context, query string, params slack.SearchParameters) (*slack.SearchMessages, error) {
				return expectedResults, nil
			},
		}

		results, err := api.SearchMessagesContext(ctx, "test query", slack.SearchParameters{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(results.Matches) != 1 {
			t.Errorf("expected 1 match, got %d", len(results.Matches))
		}
	})

	t.Run("PublishViewContext", func(t *testing.T) {
		expectedResp := &slack.ViewResponse{}

		api := &mockSlackAPI{
			publishViewFunc: func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
				return expectedResp, nil
			},
		}

		req := slack.PublishViewContextRequest{
			UserID: "U123",
		}

		resp, err := api.PublishViewContext(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp == nil {
			t.Error("expected non-nil response")
		}
	})
}
