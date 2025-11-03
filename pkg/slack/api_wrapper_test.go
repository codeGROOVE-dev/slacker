package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
)

// TestSlackAPIWrapperIntegration tests the actual slackAPIWrapper with a mock HTTP server.
func TestSlackAPIWrapperIntegration(t *testing.T) {
	t.Parallel()

	// Create a mock HTTP server that responds to Slack API calls
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return simple successful responses for all endpoints
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/team.info":
			w.Write([]byte(`{"ok":true,"team":{"id":"T123","name":"Test Team"}}`))
		case "/api/auth.test":
			w.Write([]byte(`{"ok":true,"user_id":"U123","team_id":"T123"}`))
		case "/api/conversations.info":
			w.Write([]byte(`{"ok":true,"channel":{"id":"C123","name":"test"}}`))
		case "/api/conversations.history":
			w.Write([]byte(`{"ok":true,"messages":[]}`))
		case "/api/conversations.list":
			w.Write([]byte(`{"ok":true,"channels":[]}`))
		case "/api/conversations.open":
			w.Write([]byte(`{"ok":true,"channel":{"id":"D123"}}`))
		case "/api/conversations.members":
			w.Write([]byte(`{"ok":true,"members":["U001","U002"]}`))
		case "/api/chat.postMessage":
			w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1234567890.123456"}`))
		case "/api/chat.update":
			w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1234567890.123456"}`))
		case "/api/search.messages":
			w.Write([]byte(`{"ok":true,"messages":{"matches":[]}}`))
		case "/api/reactions.add":
			w.Write([]byte(`{"ok":true}`))
		case "/api/reactions.remove":
			w.Write([]byte(`{"ok":true}`))
		case "/api/users.info":
			w.Write([]byte(`{"ok":true,"user":{"id":"U123","name":"testuser"}}`))
		case "/api/users.getPresence":
			w.Write([]byte(`{"ok":true,"presence":"active"}`))
		case "/api/views.publish":
			w.Write([]byte(`{"ok":true}`))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()

	// Create Slack client pointing to mock server
	slackClient := slack.New("test-token", slack.OptionAPIURL(server.URL+"/api/"))
	wrapper := newSlackAPIWrapper(slackClient)

	ctx := context.Background()

	t.Run("GetTeamInfoContext", func(t *testing.T) {
		info, err := wrapper.GetTeamInfoContext(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.ID != "T123" {
			t.Errorf("expected team ID T123, got %s", info.ID)
		}
	})

	t.Run("AuthTestContext", func(t *testing.T) {
		resp, err := wrapper.AuthTestContext(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.UserID != "U123" {
			t.Errorf("expected user ID U123, got %s", resp.UserID)
		}
	})

	t.Run("GetConversationInfoContext", func(t *testing.T) {
		input := &slack.GetConversationInfoInput{ChannelID: "C123"}
		ch, err := wrapper.GetConversationInfoContext(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ch.ID != "C123" {
			t.Errorf("expected channel ID C123, got %s", ch.ID)
		}
	})

	t.Run("GetConversationHistoryContext", func(t *testing.T) {
		params := &slack.GetConversationHistoryParameters{
			ChannelID: "C123",
		}
		resp, err := wrapper.GetConversationHistoryContext(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Error("expected non-nil response")
		}
	})

	t.Run("GetConversationsContext", func(t *testing.T) {
		params := &slack.GetConversationsParameters{}
		channels, _, err := wrapper.GetConversationsContext(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if channels == nil {
			t.Error("expected non-nil channels")
		}
	})

	t.Run("OpenConversationContext", func(t *testing.T) {
		params := &slack.OpenConversationParameters{
			Users: []string{"U123"},
		}
		ch, _, _, err := wrapper.OpenConversationContext(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ch.ID != "D123" {
			t.Errorf("expected channel ID D123, got %s", ch.ID)
		}
	})

	t.Run("GetUsersInConversationContext", func(t *testing.T) {
		params := &slack.GetUsersInConversationParameters{
			ChannelID: "C123",
		}
		users, _, err := wrapper.GetUsersInConversationContext(ctx, params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(users) != 2 {
			t.Errorf("expected 2 users, got %d", len(users))
		}
	})

	t.Run("PostMessageContext", func(t *testing.T) {
		_, ts, err := wrapper.PostMessageContext(ctx, "C123", slack.MsgOptionText("test", false))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ts != "1234567890.123456" {
			t.Errorf("expected timestamp 1234567890.123456, got %s", ts)
		}
	})

	t.Run("UpdateMessageContext", func(t *testing.T) {
		_, _, _, err := wrapper.UpdateMessageContext(ctx, "C123", "1234567890.123456", slack.MsgOptionText("updated", false))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("SearchMessagesContext", func(t *testing.T) {
		params := slack.SearchParameters{}
		results, err := wrapper.SearchMessagesContext(ctx, "test", params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if results == nil {
			t.Error("expected non-nil results")
		}
	})

	t.Run("AddReactionContext", func(t *testing.T) {
		item := slack.ItemRef{
			Channel:   "C123",
			Timestamp: "1234567890.123456",
		}
		err := wrapper.AddReactionContext(ctx, "thumbsup", item)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("RemoveReactionContext", func(t *testing.T) {
		item := slack.ItemRef{
			Channel:   "C123",
			Timestamp: "1234567890.123456",
		}
		err := wrapper.RemoveReactionContext(ctx, "thumbsup", item)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("GetUserInfoContext", func(t *testing.T) {
		user, err := wrapper.GetUserInfoContext(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user.ID != "U123" {
			t.Errorf("expected user ID U123, got %s", user.ID)
		}
	})

	t.Run("GetUserPresenceContext", func(t *testing.T) {
		presence, err := wrapper.GetUserPresenceContext(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if presence.Presence != "active" {
			t.Errorf("expected presence 'active', got %s", presence.Presence)
		}
	})

	t.Run("PublishViewContext", func(t *testing.T) {
		request := slack.PublishViewContextRequest{
			UserID: "U123",
			View: slack.HomeTabViewRequest{
				Type: "home",
				Blocks: slack.Blocks{
					BlockSet: []slack.Block{},
				},
			},
		}
		_, err := wrapper.PublishViewContext(ctx, request)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
