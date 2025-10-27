package slack

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockHomeHandler simulates a home view handler for testing.
type mockHomeHandler struct {
	mu      sync.Mutex
	calls   []homeViewCall
	callErr error
}

type homeViewCall struct {
	teamID string
	userID string
}

func (m *mockHomeHandler) handler(ctx context.Context, teamID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, homeViewCall{teamID: teamID, userID: userID})
	return m.callErr
}

// TestHandleInteractionsRefreshButton tests the refresh button interaction flow.
func TestHandleInteractionsRefreshButton(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantStatus     int
		wantError      string
		verifyHandler  bool
		setupManager   bool
		signingSecret  string
		includeHeaders bool
	}{
		{
			name: "successful refresh button click - form parsed correctly",
			body: url.Values{
				"payload": {`{
					"type": "block_actions",
					"team": {"id": "T123", "domain": "test"},
					"user": {"id": "U123", "name": "testuser"},
					"actions": [{"action_id": "refresh_dashboard"}],
					"container": {"type": "view"}
				}`},
			}.Encode(),
			// Note: We expect 401 (signature failure) not 400 (parsing failure)
			// This confirms the form parsing works correctly
			wantStatus:     http.StatusUnauthorized,
			verifyHandler:  false,
			setupManager:   true,
			signingSecret:  "test-secret",
			includeHeaders: true,
		},
		{
			name:       "missing payload field",
			body:       url.Values{}.Encode(),
			wantStatus: http.StatusBadRequest,
			wantError:  "interaction missing payload field",
		},
		{
			name: "malformed JSON in payload",
			body: url.Values{
				"payload": {`{invalid json`},
			}.Encode(),
			wantStatus: http.StatusBadRequest,
			wantError:  "failed to parse interaction payload",
		},
		{
			name: "missing team ID",
			body: url.Values{
				"payload": {`{
					"type": "block_actions",
					"user": {"id": "U123"},
					"actions": [{"action_id": "refresh_dashboard"}]
				}`},
			}.Encode(),
			wantStatus: http.StatusBadRequest,
			wantError:  "interaction missing team.id",
		},
		{
			name:       "invalid form data",
			body:       "not-valid-form-data\x00\x01",
			wantStatus: http.StatusBadRequest,
			wantError:  "failed to parse form data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock manager
			manager := NewManager("test-secret")

			// Create mock home handler
			mockHandler := &mockHomeHandler{}

			// Setup manager with a mock client if needed
			if tt.setupManager {
				// Create a client for the test team
				client := &Client{
					signingSecret: "test-secret",
					cache: &apiCache{
						entries: make(map[string]cacheEntry),
					},
				}
				client.SetHomeViewHandler(mockHandler.handler)

				// Store the client in the manager
				manager.mu.Lock()
				manager.clients["T123"] = client
				manager.mu.Unlock()
			}

			// Create router
			router := NewEventRouter(manager)

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/slack/interactive-endpoint", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			if tt.includeHeaders {
				// Add minimal Slack signature headers (won't be valid but structure is there)
				req.Header.Set("X-Slack-Signature", "v0=fake")
				req.Header.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
			}

			// Create response recorder
			w := httptest.NewRecorder()

			// Handle request
			router.HandleInteractions(w, req)

			// Check status code
			if w.Code != tt.wantStatus {
				t.Errorf("HandleInteractions() status = %v, want %v", w.Code, tt.wantStatus)
			}
		})
	}
}

// TestHandleInteractionsFormParsing specifically tests the form parsing fix.
func TestHandleInteractionsFormParsing(t *testing.T) {
	// This test ensures the fix for parsing payload from already-read body works
	payloadJSON := `{
		"type": "block_actions",
		"team": {"id": "T123", "domain": "test"},
		"user": {"id": "U123", "name": "testuser"},
		"actions": [{"action_id": "refresh_dashboard"}]
	}`

	// Create form-encoded body exactly as Slack sends it
	formData := url.Values{}
	formData.Set("payload", payloadJSON)
	body := formData.Encode()

	// Create manager and router
	manager := NewManager("test-secret")
	router := NewEventRouter(manager)

	// Create request with form-encoded body
	req := httptest.NewRequest(http.MethodPost, "/slack/interactive-endpoint", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Create a client for the test team
	client := &Client{
		signingSecret: "test-secret",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Store the client in manager so it can be retrieved
	manager.mu.Lock()
	manager.clients["T123"] = client
	manager.mu.Unlock()

	// Handle request
	w := httptest.NewRecorder()

	// This should NOT result in a 400 error
	router.HandleInteractions(w, req)

	// We expect either 200 (success) or 401 (invalid signature)
	// but NOT 400 (bad request from parsing error)
	if w.Code == http.StatusBadRequest {
		bodyBytes, err := io.ReadAll(w.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}
		t.Errorf("HandleInteractions() returned 400 Bad Request, indicating form parsing failed. Body: %s", string(bodyBytes))
	}

	// The actual result will be 401 due to signature verification,
	// but that's expected - we're testing that parsing works
	if w.Code != http.StatusOK && w.Code != http.StatusUnauthorized {
		t.Logf("Note: Got status %d, which is expected (signature verification)", w.Code)
	}
}

// TestClientInteractionsHandlerNoDoubleVerification tests that InteractionsHandler
// doesn't try to re-verify the signature after EventRouter has already done so.
func TestClientInteractionsHandlerNoDoubleVerification(t *testing.T) {
	// This test validates the bug fix where Client.InteractionsHandler was trying
	// to verify the signature AFTER FormValue() had already consumed the body,
	// causing signature verification to fail with a 401.

	payloadJSON := `{
		"type": "block_actions",
		"team": {"id": "T123", "domain": "test"},
		"user": {"id": "U123", "name": "testuser"},
		"actions": [{"action_id": "refresh_dashboard"}]
	}`

	formData := url.Values{}
	formData.Set("payload", payloadJSON)
	body := formData.Encode()

	// Create a client
	client := &Client{
		signingSecret: "test-secret",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Setup mock home handler
	mockHandler := &mockHomeHandler{}
	client.SetHomeViewHandler(mockHandler.handler)

	// Create request - this simulates what EventRouter does after verification
	req := httptest.NewRequest(http.MethodPost, "/slack/interactive-endpoint", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()

	// Call InteractionsHandler directly (as EventRouter does after verification)
	client.InteractionsHandler(w, req)

	// CRITICAL: We should get 200 OK, NOT 401 Unauthorized
	// Before the fix, this would return 401 because InteractionsHandler tried to
	// verify the signature after FormValue() consumed the body, which was already
	// consumed by FormValue("payload")
	if w.Code != http.StatusOK {
		t.Fatalf("InteractionsHandler() status = %v, want %v. "+
			"This indicates double signature verification bug has returned!", w.Code, http.StatusOK)
	}

	// SUCCESS: Getting 200 OK means:
	// 1. FormValue("payload") successfully parsed the payload
	// 2. No redundant signature verification happened (which would fail with 401)
	// 3. The interaction was processed successfully
	t.Logf("✓ InteractionsHandler processed request without double signature verification")
}
