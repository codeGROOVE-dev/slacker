package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

// TestNewOAuthHandler tests creating a new OAuth handler.
func TestNewOAuthHandler(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-secret")
	clientID := "test-client-id"
	clientSecret := "test-secret"

	handler := NewOAuthHandler(manager, clientID, clientSecret)

	if handler == nil {
		t.Fatal("Expected non-nil handler")
	}

	if handler.clientID != clientID {
		t.Errorf("Expected client ID %s, got %s", clientID, handler.clientID)
	}

	if handler.clientSecret != clientSecret {
		t.Errorf("Expected client secret %s, got %s", clientSecret, handler.clientSecret)
	}

	if handler.store != manager {
		t.Error("Expected store to be set to manager")
	}

	if handler.manager != manager {
		t.Error("Expected manager to be set")
	}

	if handler.exchanger == nil {
		t.Error("Expected exchanger to be set")
	}
}

// TestSlackOAuthExchanger_ExchangeCode tests the default OAuth exchanger.
func TestSlackOAuthExchanger_ExchangeCode(t *testing.T) {
	// Skip this test in regular runs as it requires real Slack API
	if testing.Short() {
		t.Skip("Skipping OAuth exchange test in short mode")
	}

	exchanger := &slackOAuthExchanger{}

	// This will fail with invalid credentials, but we're testing the code path
	_, err := exchanger.ExchangeCode(
		context.Background(),
		"invalid-client-id",
		"invalid-secret",
		"invalid-code",
	)

	// We expect an error since credentials are invalid
	if err == nil {
		t.Error("Expected error with invalid credentials")
	}
}

// TestHandleInstall tests the install page handler.
func TestHandleInstall(t *testing.T) {
	t.Parallel()

	handler := &OAuthHandler{
		clientID:     "test-client-id-123",
		clientSecret: "test-secret",
	}

	req := httptest.NewRequest(http.MethodGet, "/install", http.NoBody)
	w := httptest.NewRecorder()

	handler.HandleInstall(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected text/html content type, got %s", contentType)
	}

	body := w.Body.String()

	// Verify HTML structure
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Expected HTML doctype")
	}

	// Verify client ID is in OAuth URL
	if !strings.Contains(body, "test-client-id-123") {
		t.Error("Expected client ID in OAuth URL")
	}

	// Verify OAuth URL structure
	if !strings.Contains(body, "slack.com/oauth/v2/authorize") {
		t.Error("Expected Slack OAuth URL")
	}

	// Verify scopes are included (they're URL-encoded in the actual URL)
	if !strings.Contains(body, "scope=") {
		t.Error("Expected scope parameter in OAuth URL")
	}

	// Verify state cookie was set
	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "oauth_state" {
			stateCookie = cookie
			break
		}
	}

	if stateCookie == nil {
		t.Fatal("Expected oauth_state cookie to be set")
	}

	if stateCookie.Value == "" {
		t.Error("Expected non-empty state value")
	}

	if !stateCookie.HttpOnly {
		t.Error("Expected HttpOnly cookie")
	}

	if !stateCookie.Secure {
		t.Error("Expected Secure cookie")
	}

	if stateCookie.MaxAge != 600 {
		t.Errorf("Expected MaxAge 600, got %d", stateCookie.MaxAge)
	}
}

// TestHandleDebug tests the debug endpoint.
func TestHandleDebug(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-secret")

	// Add some test workspaces
	manager.mu.Lock()
	manager.metadata["T123"] = &WorkspaceMetadata{
		TeamID:    "T123",
		TeamName:  "Workspace 1",
		BotUserID: "UBOT123",
	}
	manager.metadata["T456"] = &WorkspaceMetadata{
		TeamID:    "T456",
		TeamName:  "Workspace 2",
		BotUserID: "UBOT456",
	}
	manager.mu.Unlock()

	handler := &OAuthHandler{
		manager: manager,
	}

	req := httptest.NewRequest(http.MethodGet, "/debug", http.NoBody)
	w := httptest.NewRecorder()

	handler.HandleDebug(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected application/json content type, got %s", contentType)
	}

	// Parse response
	var response map[string]any
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode JSON response: %v", err)
	}

	// Verify count
	count, ok := response["count"].(float64)
	if !ok {
		t.Fatal("Expected count field in response")
	}
	if count != 2 {
		t.Errorf("Expected count 2, got %v", count)
	}

	// Verify workspaces
	workspaces, ok := response["workspaces"].([]any)
	if !ok {
		t.Fatal("Expected workspaces array in response")
	}

	if len(workspaces) != 2 {
		t.Errorf("Expected 2 workspaces, got %d", len(workspaces))
	}

	// Verify workspace fields
	for _, ws := range workspaces {
		workspace, ok := ws.(map[string]any)
		if !ok {
			t.Error("Expected workspace to be object")
			continue
		}

		if workspace["team_id"] == nil {
			t.Error("Expected team_id field")
		}
		if workspace["team_name"] == nil {
			t.Error("Expected team_name field")
		}
		if workspace["bot_user_id"] == nil {
			t.Error("Expected bot_user_id field")
		}
	}
}

// TestHandleDebug_EmptyWorkspaces tests debug endpoint with no workspaces.
func TestHandleDebug_EmptyWorkspaces(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-secret")

	handler := &OAuthHandler{
		manager: manager,
	}

	req := httptest.NewRequest(http.MethodGet, "/debug", http.NoBody)
	w := httptest.NewRecorder()

	handler.HandleDebug(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]any
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode JSON response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok {
		t.Fatal("Expected count field in response")
	}
	if count != 0 {
		t.Errorf("Expected count 0, got %v", count)
	}

	workspaces, ok := response["workspaces"].([]any)
	if !ok {
		t.Fatal("Expected workspaces array in response")
	}

	if len(workspaces) != 0 {
		t.Errorf("Expected 0 workspaces, got %d", len(workspaces))
	}
}

// TestHandleCallback_RetrySuccess tests OAuth callback with retry succeeding.
func TestHandleCallback_RetrySuccess(t *testing.T) {
	t.Parallel()

	attempts := 0
	mockExchanger := &mockOAuthExchanger{
		exchangeFunc: func(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error) {
			attempts++
			// Fail first two attempts, succeed on third
			if attempts < 3 {
				return nil, http.ErrServerClosed
			}
			return &slack.OAuthV2Response{
				SlackResponse: slack.SlackResponse{
					Ok: true,
				},
				Team: slack.OAuthV2ResponseTeam{
					ID:   "T123",
					Name: "Test Workspace",
				},
				AccessToken: "xoxb-test-token",
				BotUserID:   "UBOT123",
			}, nil
		},
	}

	mockStore := &mockWorkspaceStore{
		storeFunc: func(ctx context.Context, metadata *WorkspaceMetadata, token string) error {
			return nil
		},
	}

	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    mockExchanger,
		store:        mockStore,
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code", http.NoBody)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 after retry, got %d", w.Code)
	}

	if attempts < 2 {
		t.Errorf("Expected at least 2 retry attempts, got %d", attempts)
	}
}

// TestHandleInstall_StateGeneration tests that install page generates unique state.
func TestHandleInstall_StateGeneration(t *testing.T) {
	t.Parallel()

	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
	}

	// Make two requests
	req1 := httptest.NewRequest(http.MethodGet, "/install", http.NoBody)
	w1 := httptest.NewRecorder()
	handler.HandleInstall(w1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/install", http.NoBody)
	w2 := httptest.NewRecorder()
	handler.HandleInstall(w2, req2)

	// Extract state values from cookies
	var state1, state2 string
	for _, cookie := range w1.Result().Cookies() {
		if cookie.Name == "oauth_state" {
			state1 = cookie.Value
		}
	}
	for _, cookie := range w2.Result().Cookies() {
		if cookie.Name == "oauth_state" {
			state2 = cookie.Value
		}
	}

	if state1 == "" || state2 == "" {
		t.Fatal("Expected state values to be set")
	}

	// State values should be different (cryptographically random)
	if state1 == state2 {
		t.Error("Expected different state values for different requests")
	}
}
