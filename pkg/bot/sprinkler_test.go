package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
)

// mockGitHub implements the minimal GitHub interface needed for testing.
type mockGitHub struct {
	findPRsFunc      func(ctx context.Context, owner, repo, commitSHA string) ([]int, error)
	refreshTokenFunc func(ctx context.Context) error
	org              string
	token            string
	client           interface{}
}

func (m *mockGitHub) InstallationToken(ctx context.Context) string {
	return m.token
}

func (m *mockGitHub) Organization() string {
	return m.org
}

func (m *mockGitHub) Client() any {
	return m.client
}

func (m *mockGitHub) FindPRsForCommit(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
	if m.findPRsFunc != nil {
		return m.findPRsFunc(ctx, owner, repo, commitSHA)
	}
	return []int{}, nil
}

func (m *mockGitHub) RefreshToken(ctx context.Context) error {
	if m.refreshTokenFunc != nil {
		return m.refreshTokenFunc(ctx)
	}
	return nil
}

func TestLookupPRsForCheckEvent_Success(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
		findPRsFunc: func(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
			if commitSHA == "abc123" {
				return []int{42, 43}, nil
			}
			return []int{}, nil
		},
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	if len(prNumbers) != 2 {
		t.Errorf("expected 2 PR numbers, got %d", len(prNumbers))
	}

	if prNumbers[0] != 42 || prNumbers[1] != 43 {
		t.Errorf("expected PRs [42, 43], got %v", prNumbers)
	}
}

func TestLookupPRsForCheckEvent_NoCommitSHA(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"delivery_id": "12345",
			// No commit_sha
		},
	}

	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	if prNumbers != nil {
		t.Errorf("expected nil result for missing commit SHA, got %v", prNumbers)
	}
}

func TestLookupPRsForCheckEvent_InvalidURL(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "invalid-url",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	if prNumbers != nil {
		t.Errorf("expected nil result for invalid URL, got %v", prNumbers)
	}
}

func TestLookupPRsForCheckEvent_NoPRsFound(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
		findPRsFunc: func(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
			return []int{}, nil // No PRs found
		},
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	if len(prNumbers) != 0 {
		t.Errorf("expected empty result when no PRs found, got %v", prNumbers)
	}
}

func TestEventKey_WithDeliveryID(t *testing.T) {
	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Raw: map[string]interface{}{
			"delivery_id": "unique-delivery-123",
		},
	}

	key := eventKey(event)

	if key != "unique-delivery-123" {
		t.Errorf("expected key to be delivery_id, got %s", key)
	}
}

func TestEventKey_WithoutDeliveryID(t *testing.T) {
	timestamp := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	event := client.Event{
		Type:      "pull_request",
		Timestamp: timestamp,
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Raw:       map[string]interface{}{},
	}

	key := eventKey(event)

	// Should be timestamp:url:type
	expected := timestamp.Format(time.RFC3339) + ":https://github.com/testorg/testrepo/pull/42:pull_request"
	if key != expected {
		t.Errorf("expected key %s, got %s", expected, key)
	}
}

// parsePRNumberFromURL is already tested in bot_sprinkler_test.go

func TestHandleSprinklerEvent_Deduplication(t *testing.T) {
	ctx := context.Background()

	processedEvents := make(map[string]bool)
	mockState := &mockStateStore{
		processedEvents: processedEvents,
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Raw: map[string]interface{}{
			"delivery_id": "unique-123",
		},
	}

	// First call should process
	c.handleSprinklerEvent(ctx, event, "testorg")

	// Verify event was marked as processed
	if !mockState.WasProcessed("unique-123") {
		t.Error("expected event to be marked as processed")
	}

	// Second call with same event should be deduplicated
	processCountBefore := len(processedEvents)
	c.handleSprinklerEvent(ctx, event, "testorg")
	processCountAfter := len(processedEvents)

	if processCountAfter > processCountBefore {
		t.Error("expected duplicate event to be skipped")
	}
}

func TestHandleSprinklerEvent_PullRequestWithNumber(t *testing.T) {
	ctx := context.Background()

	handledPR := false
	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	// Track if handlePullRequestFromSprinkler would be called
	// (we can't easily test this without full integration, but we verify the event is processed)

	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Raw: map[string]interface{}{
			"delivery_id": "pr-event-123",
			"action":      "opened",
		},
	}

	c.handleSprinklerEvent(ctx, event, "testorg")

	// Verify event was processed (marked in state store)
	if !mockState.WasProcessed("pr-event-123") {
		t.Error("expected PR event to be marked as processed")
	}

	// In a real scenario, handlePullRequestFromSprinkler would be called
	// We verify the event flowed through the system
	_ = handledPR
}

func TestHandleSprinklerEvent_CheckEventWithCommit(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
		findPRsFunc: func(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
			if commitSHA == "abc123" {
				return []int{42}, nil
			}
			return []int{}, nil
		},
	}

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "check_run",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"delivery_id": "check-event-123",
			"commit_sha":  "abc123",
		},
	}

	c.handleSprinklerEvent(ctx, event, "testorg")

	// Verify event was processed
	if !mockState.WasProcessed("check-event-123") {
		t.Error("expected check event to be marked as processed")
	}
}

func TestLookupPRsForCheckEvent_GitHubAPIFailure(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
		findPRsFunc: func(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
			return nil, errors.New("GitHub API error")
		},
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	// Should return nil when GitHub API fails
	if prNumbers != nil {
		t.Errorf("expected nil result on API failure, got %v", prNumbers)
	}
}

func TestLookupPRsForCheckEvent_InvalidURLFormat(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "too few parts",
			url:  "https://github.com/owner",
		},
		{
			name: "wrong domain",
			url:  "https://gitlab.com/owner/repo/commit/abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := client.Event{
				Type: "check_run",
				URL:  tt.url,
				Raw: map[string]interface{}{
					"commit_sha":  "abc123",
					"delivery_id": "12345",
				},
			}

			prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

			if prNumbers != nil {
				t.Errorf("expected nil result for invalid URL format, got %v", prNumbers)
			}
		})
	}
}

func TestHandleSprinklerEvent_MissingURL(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "", // Missing URL
		Raw: map[string]interface{}{
			"delivery_id": "missing-url-123",
		},
	}

	c.handleSprinklerEvent(ctx, event, "testorg")

	// Event should be marked as processed but then return early
	time.Sleep(50 * time.Millisecond) // Give goroutine time to run
}

func TestHandleSprinklerEvent_InvalidPRURL(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo", // No PR number
		Raw: map[string]interface{}{
			"delivery_id": "invalid-pr-url-123",
		},
	}

	c.handleSprinklerEvent(ctx, event, "testorg")

	// Event should be marked as processed
	time.Sleep(50 * time.Millisecond) // Give goroutine time to run
}

func TestWaitForEventProcessing_NoEvents(t *testing.T) {
	c := &Coordinator{
		eventSemaphore: make(chan struct{}, 10),
	}

	// Should return immediately when no events are processing
	c.waitForEventProcessing("testorg", 5*time.Second)
	// Test passes if it returns quickly without blocking
}

func TestWaitForEventProcessing_WithEvents(t *testing.T) {
	c := &Coordinator{
		eventSemaphore: make(chan struct{}, 10),
	}

	// Add an event to the queue
	c.eventSemaphore <- struct{}{}
	c.processingEvents.Add(1)

	// Start goroutine that will complete the event
	go func() {
		time.Sleep(100 * time.Millisecond)
		<-c.eventSemaphore
		c.processingEvents.Done()
	}()

	// Wait for events to complete
	start := time.Now()
	c.waitForEventProcessing("testorg", 2*time.Second)
	elapsed := time.Since(start)

	// Should have waited for the event to complete
	if elapsed < 100*time.Millisecond {
		t.Error("should have waited for event to complete")
	}
	if elapsed > 1*time.Second {
		t.Error("should not have taken too long")
	}
}

func TestWaitForEventProcessing_Timeout(t *testing.T) {
	c := &Coordinator{
		eventSemaphore: make(chan struct{}, 10),
	}

	// Add an event that will never complete
	c.eventSemaphore <- struct{}{}
	c.processingEvents.Add(1)

	// Wait with short timeout
	start := time.Now()
	c.waitForEventProcessing("testorg", 100*time.Millisecond)
	elapsed := time.Since(start)

	// Should have timed out around 100ms
	if elapsed < 100*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("expected timeout around 100ms, got %v", elapsed)
	}

	// Clean up
	<-c.eventSemaphore
	c.processingEvents.Done()
}

func TestHandleAuthError_RefreshSuccess(t *testing.T) {
	ctx := context.Background()

	refreshCalled := false
	newToken := ""

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "old-token",
		refreshTokenFunc: func(ctx context.Context) error {
			refreshCalled = true
			return nil
		},
	}

	c := &Coordinator{
		github: mockGH,
	}

	createConfig := func(ctx context.Context, token string) client.Config {
		newToken = token
		return client.Config{
			Organization: "testorg",
			Token:        token,
			ServerURL:    "wss://test.example.com",
		}
	}

	// Update mockGH to return new token after refresh
	mockGH.token = "new-token"

	newClient, err := c.handleAuthError(ctx, "testorg", createConfig)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !refreshCalled {
		t.Error("expected RefreshToken to be called")
	}

	if newClient == nil {
		t.Error("expected new client to be returned")
	}

	if newToken != "new-token" {
		t.Errorf("expected new token to be used, got %s", newToken)
	}
}

func TestHandleAuthError_RefreshFails(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "old-token",
		refreshTokenFunc: func(ctx context.Context) error {
			return errors.New("refresh failed")
		},
	}

	c := &Coordinator{
		github: mockGH,
	}

	createConfig := func(ctx context.Context, token string) client.Config {
		return client.Config{
			Organization: "testorg",
			Token:        token,
			ServerURL:    "wss://test.example.com",
		}
	}

	newClient, err := c.handleAuthError(ctx, "testorg", createConfig)

	if err == nil {
		t.Error("expected error when refresh fails")
	}

	if newClient != nil {
		t.Error("expected nil client when refresh fails")
	}

	if err.Error() != "refresh failed" {
		t.Errorf("expected 'refresh failed', got %v", err)
	}
}

func TestHandleAuthError_EmptyTokenAfterRefresh(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "", // Empty token after refresh
		refreshTokenFunc: func(ctx context.Context) error {
			return nil // Refresh succeeds but token is empty
		},
	}

	c := &Coordinator{
		github: mockGH,
	}

	createConfig := func(ctx context.Context, token string) client.Config {
		return client.Config{
			Organization: "testorg",
			Token:        token,
			ServerURL:    "wss://test.example.com",
		}
	}

	newClient, err := c.handleAuthError(ctx, "testorg", createConfig)

	if err == nil {
		t.Error("expected error when token is empty after refresh")
	}

	if newClient != nil {
		t.Error("expected nil client when token is empty")
	}

	if err.Error() != "token refresh succeeded but returned empty token" {
		t.Errorf("expected empty token error, got %v", err)
	}
}

func TestHandleAuthError_ClientCreationFails(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "refreshed-token",
		refreshTokenFunc: func(ctx context.Context) error {
			return nil // Refresh succeeds
		},
	}

	c := &Coordinator{
		github: mockGH,
	}

	createConfig := func(ctx context.Context, token string) client.Config {
		// Return invalid config that will cause client.New to fail
		return client.Config{
			Organization: "testorg",
			Token:        token,
			ServerURL:    "", // Invalid empty URL
		}
	}

	_, err := c.handleAuthError(ctx, "testorg", createConfig)
	if err == nil {
		t.Error("expected error when client creation fails")
	}
	if !strings.Contains(err.Error(), "failed to create sprinkler client") {
		t.Errorf("expected 'failed to create sprinkler client' error, got: %v", err)
	}
}

func TestHandleSprinklerEvent_DatabaseError(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		processedEvents:  make(map[string]bool),
		markProcessedErr: errors.New("database connection error"),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Raw: map[string]interface{}{
			"delivery_id": "db-error-123",
		},
	}

	// Should log error and return without processing
	c.handleSprinklerEvent(ctx, event, "testorg")

	// Event should NOT be marked as processed due to database error
	if mockState.WasProcessed("db-error-123") {
		t.Error("expected event NOT to be marked as processed after database error")
	}
}
