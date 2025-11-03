package slack

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

// mockWorkspaceStore is a programmable mock for WorkspaceStorer.
type mockWorkspaceStore struct {
	storeFunc func(ctx context.Context, metadata *WorkspaceMetadata, token string) error
}

func (m *mockWorkspaceStore) StoreWorkspace(ctx context.Context, metadata *WorkspaceMetadata, token string) error {
	if m.storeFunc != nil {
		return m.storeFunc(ctx, metadata, token)
	}
	return nil // Default: success
}

// mockOAuthExchanger is a programmable mock for OAuthExchanger.
type mockOAuthExchanger struct {
	exchangeFunc func(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error)
}

func (m *mockOAuthExchanger) ExchangeCode(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error) {
	if m.exchangeFunc != nil {
		return m.exchangeFunc(ctx, clientID, clientSecret, code)
	}
	// Default: return error (OAuth exchange requires real Slack API)
	return nil, errors.New("mock: OAuth exchange not configured")
}

// TestHandleCallback_MissingCode tests when code parameter is missing.
func TestHandleCallback_MissingCode(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Missing code parameter") {
		t.Errorf("Expected error message about missing code, got: %s", body)
	}
}

// TestHandleCallback_ShortCode tests OAuth code logging with short value.
func TestHandleCallback_ShortCode(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	// Use very short code (< 10 chars) to test min() edge case in logging
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=abc", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	// Will fail at OAuth exchange but we're testing the code path before that
	// The important part is that the short code doesn't cause a panic
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusBadRequest {
		t.Logf("Got status %d (expected some error status)", w.Code)
	}
}

// TestHandleCallback_OAuthError tests when OAuth returns an error.
func TestHandleCallback_OAuthError(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	// Error parameter takes priority over code
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test&error=access_denied", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "OAuth error") {
		t.Errorf("Expected OAuth error message, got: %s", body)
	}
}

// TestHandleCallback_StateMismatch tests CSRF protection.
func TestHandleCallback_StateMismatch(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code&state=wrong-state", nil)
	req.AddCookie(&http.Cookie{
		Name:  "oauth_state",
		Value: "correct-state",
	})
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Invalid state parameter") {
		t.Errorf("Expected invalid state error, got: %s", body)
	}
}

// TestHandleCallback_StateMismatchShortValue tests state mismatch with short strings.
func TestHandleCallback_StateMismatchShortValue(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	// Use very short state values (< 10 chars) to test min() edge case in logging
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code&state=abc", nil)
	req.AddCookie(&http.Cookie{
		Name:  "oauth_state",
		Value: "xyz",
	})
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Invalid state parameter") {
		t.Errorf("Expected invalid state error, got: %s", body)
	}
}

// TestHandleCallback_MissingStateCookie tests when state param exists but cookie doesn't.
func TestHandleCallback_MissingStateCookie(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code&state=some-state", nil)
	// Don't add cookie
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Invalid state parameter") {
		t.Errorf("Expected invalid state error, got: %s", body)
	}
}

// TestHandleCallback_StateMatchSuccess tests successful state verification.
func TestHandleCallback_StateMatchSuccess(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code&state=matching-state", nil)
	req.AddCookie(&http.Cookie{
		Name:  "oauth_state",
		Value: "matching-state",
	})
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	// This will fail at token exchange (since we're not mocking Slack OAuth API)
	// but we can verify state checking passed by checking we got past that point
	// The error should be about token exchange, not state
	if w.Code == http.StatusBadRequest {
		body := w.Body.String()
		if strings.Contains(body, "Invalid state parameter") {
			t.Error("State verification should have passed")
		}
	}
}

// TestHandleCallback_CookieDeletion tests that state cookie is cleared after verification.
func TestHandleCallback_CookieDeletion(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code&state=matching-state", nil)
	req.AddCookie(&http.Cookie{
		Name:  "oauth_state",
		Value: "matching-state",
	})
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	// Verify the state cookie was cleared (MaxAge: -1)
	cookies := w.Result().Cookies()
	var stateCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == "oauth_state" {
			stateCookie = cookie
			break
		}
	}

	if stateCookie == nil {
		t.Error("Expected oauth_state cookie to be set for deletion")
		return
	}

	if stateCookie.MaxAge != -1 {
		t.Errorf("Expected oauth_state cookie MaxAge to be -1 (deleted), got %d", stateCookie.MaxAge)
	}

	if stateCookie.Value != "" {
		t.Errorf("Expected oauth_state cookie value to be empty, got %q", stateCookie.Value)
	}

	if !stateCookie.HttpOnly {
		t.Error("Expected oauth_state cookie to be HttpOnly")
	}

	if !stateCookie.Secure {
		t.Error("Expected oauth_state cookie to be Secure")
	}
}

// TestHandleCallback_NoStateParam tests direct installation without state.
func TestHandleCallback_NoStateParam(t *testing.T) {
	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    &mockOAuthExchanger{},
		store:        &mockWorkspaceStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=test-code", nil)
	// No state parameter, no cookie
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	// Should proceed without state checking (Slack App Directory flow)
	// Will fail at token exchange but that's expected
	if w.Code == http.StatusBadRequest {
		body := w.Body.String()
		if strings.Contains(body, "Invalid state parameter") {
			t.Error("Should allow installation without state parameter")
		}
	}
}

// TestHandleCallback_StoreWorkspaceError tests workspace storage failure.
func TestHandleCallback_StoreWorkspaceError(t *testing.T) {
	// Create mocks - OAuth succeeds but storage fails
	mockExchanger := &mockOAuthExchanger{
		exchangeFunc: func(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error) {
			return &slack.OAuthV2Response{
				SlackResponse: slack.SlackResponse{
					Ok: true,
				},
				Team: slack.OAuthV2ResponseTeam{
					ID:   "T12345",
					Name: "Test Workspace",
				},
				AccessToken: "xoxb-test-token",
				BotUserID:   "U123BOT",
			}, nil
		},
	}

	mockStore := &mockWorkspaceStore{
		storeFunc: func(ctx context.Context, metadata *WorkspaceMetadata, token string) error {
			return errors.New("storage failure")
		},
	}

	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    mockExchanger,
		store:        mockStore,
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=valid-code", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	// Should return 500 due to storage failure
	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Failed to store credentials") {
		t.Errorf("Expected storage error message, got: %s", body)
	}
}

// TestHandleCallback_OAuthNotOk tests OAuth response with Ok: false.
func TestHandleCallback_OAuthNotOk(t *testing.T) {
	mockExchanger := &mockOAuthExchanger{
		exchangeFunc: func(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error) {
			return &slack.OAuthV2Response{
				SlackResponse: slack.SlackResponse{
					Ok:    false,
					Error: "invalid_code",
				},
			}, nil
		},
	}

	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    mockExchanger,
		store:        &mockWorkspaceStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=invalid-code", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "OAuth error") {
		t.Errorf("Expected OAuth error message, got: %s", body)
	}
}

// TestHandleCallback_SuccessfulFlow tests complete OAuth success flow.
func TestHandleCallback_SuccessfulFlow(t *testing.T) {
	var storedMetadata *WorkspaceMetadata
	var storedToken string

	mockExchanger := &mockOAuthExchanger{
		exchangeFunc: func(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error) {
			return &slack.OAuthV2Response{
				SlackResponse: slack.SlackResponse{
					Ok: true,
				},
				Team: slack.OAuthV2ResponseTeam{
					ID:   "T12345",
					Name: "Test Workspace",
				},
				AccessToken: "xoxb-test-token",
				BotUserID:   "U123BOT",
				Scope:       "channels:read,chat:write",
			}, nil
		},
	}

	mockStore := &mockWorkspaceStore{
		storeFunc: func(ctx context.Context, metadata *WorkspaceMetadata, token string) error {
			storedMetadata = metadata
			storedToken = token
			return nil
		},
	}

	handler := &OAuthHandler{
		clientID:     "test-client-id",
		clientSecret: "test-secret",
		exchanger:    mockExchanger,
		store:        mockStore,
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=valid-code", nil)
	w := httptest.NewRecorder()

	handler.HandleCallback(w, req)

	// Should return 200 with success page
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Verify metadata was stored
	if storedMetadata == nil {
		t.Fatal("Expected metadata to be stored")
	}
	if storedMetadata.TeamID != "T12345" {
		t.Errorf("Expected TeamID T12345, got %s", storedMetadata.TeamID)
	}
	if storedMetadata.TeamName != "Test Workspace" {
		t.Errorf("Expected TeamName 'Test Workspace', got %s", storedMetadata.TeamName)
	}
	if storedMetadata.BotUserID != "U123BOT" {
		t.Errorf("Expected BotUserID U123BOT, got %s", storedMetadata.BotUserID)
	}
	if storedToken != "xoxb-test-token" {
		t.Errorf("Expected token 'xoxb-test-token', got %s", storedToken)
	}

	// Verify success page HTML
	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Expected HTML success page")
	}
	if !strings.Contains(body, "Test Workspace") {
		t.Error("Expected workspace name in success page")
	}
}

// TestWriteSuccessPage tests HTML success page rendering.
func TestWriteSuccessPage(t *testing.T) {
	handler := &OAuthHandler{}

	w := httptest.NewRecorder()
	handler.writeSuccessPage(w, "Test Workspace")

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected text/html content type, got %s", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Expected HTML doctype")
	}

	if !strings.Contains(body, "Test Workspace") {
		t.Error("Expected workspace name in output")
	}

	if !strings.Contains(body, "Installation Complete") || !strings.Contains(body, "Success") {
		t.Error("Expected success message in output")
	}
}

// TestWriteSuccessPage_EmptyWorkspaceName tests with empty workspace name.
func TestWriteSuccessPage_EmptyWorkspaceName(t *testing.T) {
	handler := &OAuthHandler{}

	w := httptest.NewRecorder()
	handler.writeSuccessPage(w, "")

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Expected HTML doctype even with empty workspace name")
	}
}

// TestWriteInstallPage tests HTML install page rendering.
func TestWriteInstallPage(t *testing.T) {
	handler := &OAuthHandler{}

	authURL := "https://slack.com/oauth/v2/authorize?client_id=test&scope=test"
	w := httptest.NewRecorder()
	handler.writeInstallPage(w, authURL)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected text/html content type, got %s", contentType)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Expected HTML doctype")
	}

	if !strings.Contains(body, authURL) {
		t.Error("Expected auth URL in output")
	}

	if !strings.Contains(body, "Install Ready to Review") || !strings.Contains(body, "Add to Slack") {
		t.Error("Expected install button/text in output")
	}
}

// TestWriteInstallPage_EmptyAuthURL tests with empty auth URL.
func TestWriteInstallPage_EmptyAuthURL(t *testing.T) {
	handler := &OAuthHandler{}

	w := httptest.NewRecorder()
	handler.writeInstallPage(w, "")

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Expected HTML doctype even with empty auth URL")
	}
}

// TestWriteInstallPage_SpecialCharactersInURL tests URL with special characters.
func TestWriteInstallPage_SpecialCharactersInURL(t *testing.T) {
	handler := &OAuthHandler{}

	authURL := "https://slack.com/oauth?param1=value1&param2=value2&redirect_uri=https://example.com/callback"
	w := httptest.NewRecorder()
	handler.writeInstallPage(w, authURL)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	// URL should be in the output (possibly HTML-escaped)
	if !strings.Contains(body, "slack.com/oauth") {
		t.Error("Expected auth URL domain in output")
	}
}
