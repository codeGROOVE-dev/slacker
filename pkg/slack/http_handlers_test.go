package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// generateValidSignature creates a valid Slack signature for testing.
func generateValidSignature(secret, timestamp, body string) string {
	sig := fmt.Sprintf("v0:%s:%s", timestamp, body)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(sig))
	return "v0=" + hex.EncodeToString(h.Sum(nil))
}

// TestEventsHandler_URLVerification tests the URL verification challenge.
func TestEventsHandler_URLVerification(t *testing.T) {
	t.Parallel()

	challenge := "test-challenge-string"
	body := map[string]interface{}{
		"type":      "url_verification",
		"challenge": challenge,
		"token":     "test-token",
	}
	bodyBytes, _ := json.Marshal(body)

	client := &Client{
		signingSecret: "test-secret",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(bodyBytes))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := generateValidSignature("test-secret", timestamp, string(bodyBytes))
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Body.String() != challenge {
		t.Errorf("Expected challenge response %q, got %q", challenge, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/plain" {
		t.Errorf("Expected Content-Type text/plain, got %s", contentType)
	}
}

// TestEventsHandler_InvalidSignature tests signature verification failure.
func TestEventsHandler_InvalidSignature(t *testing.T) {
	t.Parallel()

	body := []byte(`{"type":"url_verification","challenge":"test"}`)

	client := &Client{
		signingSecret: "test-secret",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(body))
	req.Header.Set("X-Slack-Signature", "v0=invalid")
	req.Header.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// TestEventsHandler_ReadBodyError tests when body reading fails.
func TestEventsHandler_ReadBodyError(t *testing.T) {
	t.Parallel()

	client := &Client{
		signingSecret: "test-secret",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Create a request with a body that will fail to read
	req := httptest.NewRequest(http.MethodPost, "/slack/events", &errorReader{})
	w := httptest.NewRecorder()

	client.EventsHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// errorReader is a reader that always returns an error.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("read error")
}

// TestEventsHandler_ParseEventError tests handling of malformed event JSON.
func TestEventsHandler_ParseEventError(t *testing.T) {
	t.Parallel()

	body := []byte(`{invalid json`)

	client := &Client{
		signingSecret: "test-secret",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(body))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := generateValidSignature("test-secret", timestamp, string(body))
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// TestEventsHandler_URLVerificationUnmarshalError tests challenge unmarshal error.
func TestEventsHandler_URLVerificationUnmarshalError(t *testing.T) {
	t.Parallel()

	// Create a URL verification event but with malformed challenge field
	body := []byte(`{"type":"url_verification","challenge":123}`)

	client := &Client{
		signingSecret: "test-secret",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(body))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := generateValidSignature("test-secret", timestamp, string(body))
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	// Parse fails first, so we get 400
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// TestEventsHandler_AppHomeOpened tests app_home_opened event handling.
func TestEventsHandler_AppHomeOpened(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	handlerCalled := false
	var capturedTeamID, capturedUserID string
	done := make(chan bool, 1)

	client := &Client{
		signingSecret: "test-secret",
		teamID:        "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Set up home view handler
	client.SetHomeViewHandler(func(ctx context.Context, teamID, userID string) error {
		mu.Lock()
		handlerCalled = true
		capturedTeamID = teamID
		capturedUserID = userID
		mu.Unlock()
		done <- true
		return nil
	})

	// Create app_home_opened event - must be raw JSON for the parser
	bodyBytes := []byte(`{
		"token": "test-token",
		"team_id": "T123",
		"api_app_id": "A123",
		"type": "event_callback",
		"event": {
			"type": "app_home_opened",
			"user": "U456",
			"channel": "D123",
			"tab": "home",
			"event_ts": "1234567890.123456"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(bodyBytes))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := generateValidSignature("test-secret", timestamp, string(bodyBytes))
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Wait for handler to complete
	select {
	case <-done:
		// Handler completed
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for handler to be called")
	}

	mu.Lock()
	defer mu.Unlock()
	if !handlerCalled {
		t.Error("Expected home view handler to be called")
	}
	if capturedTeamID != "T123" {
		t.Errorf("Expected teamID T123, got %s", capturedTeamID)
	}
	if capturedUserID != "U456" {
		t.Errorf("Expected userID U456, got %s", capturedUserID)
	}
}

// TestEventsHandler_MessageEvent tests message event handling.
func TestEventsHandler_MessageEvent(t *testing.T) {
	t.Parallel()

	client := &Client{
		signingSecret: "test-secret",
		teamID:        "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Create message event
	bodyBytes := []byte(`{
		"token": "test-token",
		"team_id": "T123",
		"api_app_id": "A123",
		"type": "event_callback",
		"event": {
			"type": "message",
			"user": "U123",
			"text": "Hello",
			"channel": "C123",
			"ts": "1234567890.123456"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(bodyBytes))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := generateValidSignature("test-secret", timestamp, string(bodyBytes))
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

// TestEventsHandler_AppMentionEvent tests app_mention event handling.
func TestEventsHandler_AppMentionEvent(t *testing.T) {
	t.Parallel()

	client := &Client{
		signingSecret: "test-secret",
		teamID:        "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Create app_mention event
	bodyBytes := []byte(`{
		"token": "test-token",
		"team_id": "T123",
		"api_app_id": "A123",
		"type": "event_callback",
		"event": {
			"type": "app_mention",
			"user": "U123",
			"text": "Hello <@BOTID>",
			"channel": "C123",
			"ts": "1234567890.123456"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(bodyBytes))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := generateValidSignature("test-secret", timestamp, string(bodyBytes))
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

// TestEventsHandler_AppHomeOpenedNoHandler tests when no handler is registered.
func TestEventsHandler_AppHomeOpenedNoHandler(t *testing.T) {
	t.Parallel()

	client := &Client{
		signingSecret: "test-secret",
		teamID:        "T123",
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	// Don't set a home view handler

	// Create app_home_opened event
	bodyBytes := []byte(`{
		"token": "test-token",
		"team_id": "T123",
		"api_app_id": "A123",
		"type": "event_callback",
		"event": {
			"type": "app_home_opened",
			"user": "U456",
			"channel": "D123",
			"tab": "home",
			"event_ts": "1234567890.123456"
		}
	}`)

	req := httptest.NewRequest(http.MethodPost, "/slack/events", bytes.NewBuffer(bodyBytes))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := generateValidSignature("test-secret", timestamp, string(bodyBytes))
	req.Header.Set("X-Slack-Signature", signature)
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)

	w := httptest.NewRecorder()
	client.EventsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}
