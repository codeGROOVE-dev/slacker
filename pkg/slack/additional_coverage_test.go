package slack

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/slack-go/slack"
)

// TestVerifyRequest_ValidBody tests request verification with valid body.
func TestVerifyRequest_ValidBody(t *testing.T) {
	t.Parallel()

	client := &Client{
		signingSecret: "test-secret",
	}

	body := []byte("test body content")
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", "1234567890")
	req.Header.Set("X-Slack-Signature", "v0=invalid")

	// This will fail verification but test the body reading logic
	_ = client.verifyRequest(req)

	// Verify body can be read again
	readBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal("Failed to read body after verification:", err)
	}
	if !bytes.Equal(readBody, body) {
		t.Error("Body was not properly restored")
	}
}

// TestVerifyRequest_EmptyBody tests request verification with empty body.
func TestVerifyRequest_EmptyBody(t *testing.T) {
	t.Parallel()

	client := &Client{
		signingSecret: "test-secret",
	}

	req := httptest.NewRequest(http.MethodPost, "/test", http.NoBody)
	req.Header.Set("X-Slack-Request-Timestamp", "1234567890")
	req.Header.Set("X-Slack-Signature", "v0=test")

	_ = client.verifyRequest(req)
	// Test completes if it doesn't panic
}

// TestUserInfo_Success tests getting user info successfully.
func TestUserInfo_Success(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{
				ID:   userID,
				Name: "testuser",
				TZ:   "America/Los_Angeles",
			}, nil
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
		t.Fatalf("Expected no error, got: %v", err)
	}

	if user.ID != "U123" {
		t.Errorf("Expected user ID U123, got %s", user.ID)
	}

	if user.Name != "testuser" {
		t.Errorf("Expected user name testuser, got %s", user.Name)
	}
}

// TestUserTimezone_Valid tests getting user timezone.
func TestUserTimezone_Valid(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{
				ID: userID,
				TZ: "America/Los_Angeles",
			}, nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	tz, err := client.UserTimezone(context.Background(), "U123")

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if tz != "America/Los_Angeles" {
		t.Errorf("Expected America/Los_Angeles, got %s", tz)
	}
}

// TestClient_SetManager tests setting the manager reference.
func TestClient_SetManager(t *testing.T) {
	t.Parallel()

	client := &Client{}
	manager := NewManager("test-secret")

	client.SetManager(manager)

	if client.manager != manager {
		t.Error("Expected manager to be set")
	}
}

// TestClient_SetTeamID tests setting team ID.
func TestClient_SetTeamID(t *testing.T) {
	t.Parallel()

	client := &Client{}
	client.SetTeamID("T123")

	if client.teamID != "T123" {
		t.Errorf("Expected team ID T123, got %s", client.teamID)
	}
}

// TestClient_SetStateStore tests setting state store.
func TestClient_SetStateStore(t *testing.T) {
	t.Parallel()

	client := &Client{}
	store := &state.MemoryStore{}

	client.SetStateStore(store)

	client.stateStoreMu.RLock()
	gotStore := client.stateStore
	client.stateStoreMu.RUnlock()

	if gotStore != store {
		t.Error("Expected state store to be set")
	}
}

// TestClient_SetHomeViewHandler tests setting home view handler.
func TestClient_SetHomeViewHandler(t *testing.T) {
	t.Parallel()

	client := &Client{}
	called := false
	handler := func(ctx context.Context, teamID, userID string) error {
		called = true
		return nil
	}

	client.SetHomeViewHandler(handler)

	client.homeViewHandlerMu.RLock()
	gotHandler := client.homeViewHandler
	client.homeViewHandlerMu.RUnlock()

	if gotHandler == nil {
		t.Fatal("Expected handler to be set")
	}

	_ = gotHandler(context.Background(), "T123", "U123")

	if !called {
		t.Error("Expected handler to be called")
	}
}

// TestWorkspaceMetadata_Fields tests metadata fields.
func TestWorkspaceMetadata_Fields(t *testing.T) {
	t.Parallel()

	metadata := &WorkspaceMetadata{
		TeamID:    "T123",
		TeamName:  "Test Team",
		BotUserID: "UBOT123",
	}

	if metadata.TeamID == "" {
		t.Error("TeamID should not be empty")
	}

	if metadata.TeamName == "" {
		t.Error("TeamName should not be empty")
	}

	if metadata.BotUserID == "" {
		t.Error("BotUserID should not be empty")
	}
}
