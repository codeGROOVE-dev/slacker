package slack

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// TestResolveChannelID_ChannelIDInput tests when input is already a channel ID.
func TestResolveChannelID_ChannelIDInput(t *testing.T) {
	ctx := context.Background()

	// No API calls should be made
	api := &mockSlackAPI{}

	client := &Client{
		api: api,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Test with channel ID (starts with C)
	id := client.ResolveChannelID(ctx, "C123456")
	if id != "C123456" {
		t.Errorf("expected C123456, got %s", id)
	}
}

// TestResolveChannelID_HashPrefix tests when input has # prefix.
func TestResolveChannelID_HashPrefix(t *testing.T) {
	ctx := context.Background()

	api := &mockSlackAPI{
		getConversationsFunc: func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
			return []slack.Channel{
				{
					GroupConversation: slack.GroupConversation{
						Conversation: slack.Conversation{
							ID: "C123",
						},
						Name: "test-channel",
					},
				},
			}, "", nil
		},
	}

	client := &Client{
		api: api,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Test with # prefix
	id := client.ResolveChannelID(ctx, "#test-channel")
	if id != "C123" {
		t.Errorf("expected C123, got %s", id)
	}
}

// TestResolveChannelID_CacheTypeMismatch tests handling of wrong cache type.
func TestResolveChannelID_CacheTypeMismatch(t *testing.T) {
	ctx := context.Background()

	callCount := 0
	api := &mockSlackAPI{
		getConversationsFunc: func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
			callCount++
			return []slack.Channel{
				{
					GroupConversation: slack.GroupConversation{
						Conversation: slack.Conversation{
							ID: "C123",
						},
						Name: "test-channel",
					},
				},
			}, "", nil
		},
	}

	client := &Client{
		api: api,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Pre-populate cache with wrong type
	client.cache.set("channel_resolution_test-channel", 12345, 5*time.Minute) // Integer instead of string

	// Should invalidate cache and fetch from API
	id := client.ResolveChannelID(ctx, "test-channel")
	if id != "C123" {
		t.Errorf("expected C123, got %s", id)
	}

	if callCount != 1 {
		t.Errorf("expected 1 API call after cache invalidation, got %d", callCount)
	}
}

// TestResolveChannelID_FallbackToPublicOnly tests fallback to public channels only.
func TestResolveChannelID_FallbackToPublicOnly(t *testing.T) {
	ctx := context.Background()

	callCount := 0
	api := &mockSlackAPI{
		getConversationsFunc: func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
			callCount++
			// First call (public+private) fails with permission error
			if callCount == 1 {
				return nil, "", errors.New("missing_scope: channels:read")
			}
			// Second call (public only) succeeds
			return []slack.Channel{
				{
					GroupConversation: slack.GroupConversation{
						Conversation: slack.Conversation{
							ID: "C123",
						},
						Name: "test-channel",
					},
				},
			}, "", nil
		},
	}

	client := &Client{
		api: api,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Should fallback to public only after first failure
	id := client.ResolveChannelID(ctx, "test-channel")
	if id != "C123" {
		t.Errorf("expected C123 from fallback, got %s", id)
	}

	if callCount != 2 {
		t.Errorf("expected 2 API calls (first fails, second succeeds), got %d", callCount)
	}
}

// TestResolveChannelID_EmptyChannelName tests empty channel name handling.
func TestResolveChannelID_EmptyChannelName(t *testing.T) {
	ctx := context.Background()

	api := &mockSlackAPI{}

	client := &Client{
		api: api,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Empty channel name should be handled gracefully
	id := client.ResolveChannelID(ctx, "")
	if id != "" {
		t.Errorf("expected empty string, got %s", id)
	}
}

// TestHandleBlockAction_RefreshButton tests the refresh_dashboard action.
func TestHandleBlockAction_RefreshButton(t *testing.T) {
	handlerDone := make(chan struct{})
	var capturedTeamID, capturedUserID string

	client := &Client{
		homeViewHandler: func(ctx context.Context, teamID, userID string) error {
			capturedTeamID = teamID
			capturedUserID = userID
			close(handlerDone)
			return nil
		},
	}

	interaction := &slack.InteractionCallback{
		Team: slack.Team{
			ID: "T123",
		},
		User: slack.User{
			ID: "U123",
		},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{
				{
					ActionID: "refresh_dashboard",
				},
			},
		},
	}

	// Call handler
	client.handleBlockAction(interaction)

	// Wait for handler to complete or timeout
	select {
	case <-handlerDone:
		// Success
	case <-time.After(50 * time.Millisecond):
		t.Error("handler was not called within timeout")
		return
	}

	if capturedTeamID != "T123" {
		t.Errorf("expected team ID T123, got %s", capturedTeamID)
	}

	if capturedUserID != "U123" {
		t.Errorf("expected user ID U123, got %s", capturedUserID)
	}
}

// TestHandleBlockAction_RefreshButtonNoHandler tests refresh with no handler registered.
func TestHandleBlockAction_RefreshButtonNoHandler(t *testing.T) {
	client := &Client{
		homeViewHandler: nil, // No handler
	}

	interaction := &slack.InteractionCallback{
		Team: slack.Team{
			ID: "T123",
		},
		User: slack.User{
			ID: "U123",
		},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{
				{
					ActionID: "refresh_dashboard",
				},
			},
		},
	}

	// Should complete without panic
	client.handleBlockAction(interaction)

	// Give time for any potential goroutine
	time.Sleep(10 * time.Millisecond)

	// Test passes if no panic
}

// TestHandleBlockAction_RefreshButtonHandlerError tests error handling in refresh.
func TestHandleBlockAction_RefreshButtonHandlerError(t *testing.T) {
	handlerDone := make(chan struct{})

	client := &Client{
		homeViewHandler: func(ctx context.Context, teamID, userID string) error {
			close(handlerDone)
			return errors.New("handler error")
		},
	}

	interaction := &slack.InteractionCallback{
		Team: slack.Team{
			ID: "T123",
		},
		User: slack.User{
			ID: "U123",
		},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{
				{
					ActionID: "refresh_dashboard",
				},
			},
		},
	}

	// Call handler
	client.handleBlockAction(interaction)

	// Wait for handler to complete or timeout
	select {
	case <-handlerDone:
		// Success - error was handled gracefully
	case <-time.After(50 * time.Millisecond):
		t.Error("handler was not called within timeout")
	}
}

// TestHandleBlockAction_UnhandledAction tests handling of unknown action IDs.
func TestHandleBlockAction_UnhandledAction(t *testing.T) {
	client := &Client{
		homeViewHandler: func(ctx context.Context, teamID, userID string) error {
			t.Error("handler should not be called for unhandled action")
			return nil
		},
	}

	interaction := &slack.InteractionCallback{
		Team: slack.Team{
			ID: "T123",
		},
		User: slack.User{
			ID: "U123",
		},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{
				{
					ActionID: "unknown_action",
				},
			},
		},
	}

	// Should complete without calling handler
	client.handleBlockAction(interaction)

	// Give time for any potential goroutine
	time.Sleep(10 * time.Millisecond)

	// Test passes if handler not called
}

// TestHandleBlockAction_MultipleActions tests handling multiple actions in one callback.
func TestHandleBlockAction_MultipleActions(t *testing.T) {
	handlerCalls := make(chan struct{}, 2) // Buffer for 2 calls

	client := &Client{
		homeViewHandler: func(ctx context.Context, teamID, userID string) error {
			handlerCalls <- struct{}{}
			return nil
		},
	}

	interaction := &slack.InteractionCallback{
		Team: slack.Team{
			ID: "T123",
		},
		User: slack.User{
			ID: "U123",
		},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{
				{
					ActionID: "refresh_dashboard",
				},
				{
					ActionID: "unknown_action",
				},
				{
					ActionID: "refresh_dashboard",
				},
			},
		},
	}

	// Call handler
	client.handleBlockAction(interaction)

	// Wait for both handler calls
	timeout := time.After(50 * time.Millisecond)
	callCount := 0
	for callCount < 2 {
		select {
		case <-handlerCalls:
			callCount++
		case <-timeout:
			t.Errorf("expected 2 handler calls, got %d within timeout", callCount)
			return
		}
	}

	// Verify we got exactly 2 calls
	if callCount != 2 {
		t.Errorf("expected 2 handler calls, got %d", callCount)
	}
}

// TestHandleBlockAction_EmptyActions tests handling of empty actions list.
func TestHandleBlockAction_EmptyActions(t *testing.T) {
	client := &Client{
		homeViewHandler: func(ctx context.Context, teamID, userID string) error {
			t.Error("handler should not be called for empty actions")
			return nil
		},
	}

	interaction := &slack.InteractionCallback{
		Team: slack.Team{
			ID: "T123",
		},
		User: slack.User{
			ID: "U123",
		},
		ActionCallback: slack.ActionCallbacks{
			BlockActions: []*slack.BlockAction{}, // Empty
		},
	}

	// Should complete without calling handler
	client.handleBlockAction(interaction)

	// Give time for any potential goroutine
	time.Sleep(10 * time.Millisecond)

	// Test passes if handler not called
}

// TestResolveChannelID_Pagination tests channel resolution with multiple pages.
func TestResolveChannelID_Pagination(t *testing.T) {
	ctx := context.Background()

	callCount := 0
	api := &mockSlackAPI{
		getConversationsFunc: func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
			callCount++
			// First call returns page 1 with cursor
			if callCount == 1 {
				return []slack.Channel{
					{
						GroupConversation: slack.GroupConversation{
							Conversation: slack.Conversation{
								ID: "C111",
							},
							Name: "channel-one",
						},
					},
				}, "cursor_page2", nil
			}
			// Second call returns page 2 with target channel
			if callCount == 2 {
				return []slack.Channel{
					{
						GroupConversation: slack.GroupConversation{
							Conversation: slack.Conversation{
								ID: "C222",
							},
							Name: "target-channel",
						},
					},
				}, "", nil
			}
			return nil, "", errors.New("unexpected call")
		},
	}

	client := &Client{
		api:        api,
		retryDelay: 1 * time.Millisecond,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Should find channel on second page
	id := client.ResolveChannelID(ctx, "target-channel")
	if id != "C222" {
		t.Errorf("expected C222, got %s", id)
	}

	if callCount != 2 {
		t.Errorf("expected 2 API calls for pagination, got %d", callCount)
	}
}

// TestResolveChannelID_PaginationError tests error during pagination.
func TestResolveChannelID_PaginationError(t *testing.T) {
	ctx := context.Background()

	callCount := 0
	api := &mockSlackAPI{
		getConversationsFunc: func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
			callCount++
			// First call returns page 1 with cursor
			if callCount == 1 {
				return []slack.Channel{
					{
						GroupConversation: slack.GroupConversation{
							Conversation: slack.Conversation{
								ID: "C111",
							},
							Name: "channel-one",
						},
					},
				}, "cursor_page2", nil
			}
			// Second call fails
			return nil, "", errors.New("api error during pagination")
		},
	}

	client := &Client{
		api:        api,
		retryDelay: 1 * time.Millisecond,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Should return original name when pagination fails
	id := client.ResolveChannelID(ctx, "target-channel")
	if id != "target-channel" {
		t.Errorf("expected target-channel (original name), got %s", id)
	}
}

// TestResolveChannelID_ChannelNotFound tests when channel doesn't exist.
func TestResolveChannelID_ChannelNotFound(t *testing.T) {
	ctx := context.Background()

	api := &mockSlackAPI{
		getConversationsFunc: func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
			// Return channels but none matching
			return []slack.Channel{
				{
					GroupConversation: slack.GroupConversation{
						Conversation: slack.Conversation{
							ID: "C111",
						},
						Name: "other-channel",
					},
				},
			}, "", nil
		},
	}

	client := &Client{
		api:        api,
		retryDelay: 1 * time.Millisecond,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Should return original name when channel not found
	id := client.ResolveChannelID(ctx, "nonexistent-channel")
	if id != "nonexistent-channel" {
		t.Errorf("expected nonexistent-channel (original name), got %s", id)
	}
}

// TestResolveChannelID_BothFallbacksFail tests when both public+private and public-only fail.
func TestResolveChannelID_BothFallbacksFail(t *testing.T) {
	ctx := context.Background()

	callCount := 0
	api := &mockSlackAPI{
		getConversationsFunc: func(ctx context.Context, params *slack.GetConversationsParameters) ([]slack.Channel, string, error) {
			callCount++
			// Both calls fail
			return nil, "", errors.New("api error")
		},
	}

	client := &Client{
		api:        api,
		retryDelay: 1 * time.Millisecond,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Should return original name when both fallbacks fail
	id := client.ResolveChannelID(ctx, "test-channel")
	if id != "test-channel" {
		t.Errorf("expected test-channel (original name), got %s", id)
	}

	// Should have tried twice (public+private, then public only)
	if callCount < 2 {
		t.Errorf("expected at least 2 API calls for fallback, got %d", callCount)
	}
}

// TestInteractionsHandler_ViewSubmission tests handling of view submission interactions.
func TestInteractionsHandler_ViewSubmission(t *testing.T) {
	client := &Client{
		signingSecret: "test-secret",
	}

	payload := `{
		"type": "view_submission",
		"team": {"id": "T123", "domain": "test"},
		"user": {"id": "U123", "name": "testuser"},
		"view": {
			"id": "V123",
			"type": "modal",
			"title": {"type": "plain_text", "text": "Test Modal"}
		}
	}`

	formData := url.Values{}
	formData.Set("payload", payload)

	req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	client.InteractionsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

// TestInteractionsHandler_UnknownType tests handling of unknown interaction types.
func TestInteractionsHandler_UnknownType(t *testing.T) {
	client := &Client{
		signingSecret: "test-secret",
	}

	payload := `{
		"type": "unknown_interaction_type",
		"team": {"id": "T123", "domain": "test"},
		"user": {"id": "U123", "name": "testuser"}
	}`

	formData := url.Values{}
	formData.Set("payload", payload)

	req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	client.InteractionsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

// TestInteractionsHandler_MissingPayload tests error handling for missing payload.
func TestInteractionsHandler_MissingPayload(t *testing.T) {
	client := &Client{
		signingSecret: "test-secret",
	}

	// Empty form data - no payload
	req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	client.InteractionsHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// TestInteractionsHandler_InvalidJSON tests error handling for invalid JSON.
func TestInteractionsHandler_InvalidJSON(t *testing.T) {
	client := &Client{
		signingSecret: "test-secret",
	}

	formData := url.Values{}
	formData.Set("payload", "invalid json {{{")

	req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	client.InteractionsHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}
