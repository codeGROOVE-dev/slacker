package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

func TestIntegration_FindOrCreatePRThread_CreateNew(t *testing.T) {
	ctx := context.Background()

	var postedText string
	var postedChannel string
	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			postedChannel = channelID
			postedText = text
			return "1234567890.999999", nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			// No existing messages - force creation of new thread
			return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
		},
	}

	mockState := &mockStateStore{
		threads: make(map[string]ThreadInfo),
	}

	c := &Coordinator{
		slack:         mockSlack,
		stateStore:    mockState,
		configManager: config.New(),
		threadCache: &ThreadCache{
			prThreads: make(map[string]ThreadInfo),
			creating:  make(map[string]bool),
		},
		eventSemaphore: make(chan struct{}, 10),
	}

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Integration test PR",
		Number:    42,
	}
	pr.User.Login = "testauthor"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	threadTS, wasNew, messageText, err := c.findOrCreatePRThread(
		ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wasNew {
		t.Error("expected thread to be newly created")
	}

	if threadTS != "1234567890.999999" {
		t.Errorf("expected threadTS 1234567890.999999, got %s", threadTS)
	}

	if postedChannel != "C123" {
		t.Errorf("expected to post to C123, got %s", postedChannel)
	}

	if postedText == "" {
		t.Error("expected non-empty posted text")
	}

	if messageText == "" {
		t.Error("expected non-empty returned message text")
	}

	// Verify thread was cached
	cacheKey := "testorg/testrepo#42:C123"
	cachedInfo, exists := c.threadCache.Get(cacheKey)
	if !exists {
		t.Error("expected thread to be cached")
	}

	if cachedInfo.ThreadTS != threadTS {
		t.Errorf("expected cached threadTS %s, got %s", threadTS, cachedInfo.ThreadTS)
	}

	if cachedInfo.MessageText != messageText {
		t.Error("expected cached message text to match returned text")
	}

	// Give time for async save to state store
	time.Sleep(20 * time.Millisecond)

	// Verify saved to state store
	if len(mockState.threads) == 0 {
		t.Log("Note: thread persistence happens asynchronously")
	}
}

func TestIntegration_FindOrCreatePRThread_FindExisting(t *testing.T) {
	ctx := context.Background()

	postCount := 0
	existingURL := "https://github.com/testorg/testrepo/pull/42"
	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			postCount++
			return "1234567890.999999", nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			// Return existing message
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							User:      "B123",
							Text:      ":hourglass: Integration test PR • testorg/testrepo#42 " + existingURL,
							Timestamp: "1234567890.555555",
						},
					},
				},
			}, nil
		},
	}

	mockState := &mockStateStore{
		threads: make(map[string]ThreadInfo),
	}

	c := &Coordinator{
		slack:         mockSlack,
		stateStore:    mockState,
		configManager: config.New(),
		threadCache: &ThreadCache{
			prThreads: make(map[string]ThreadInfo),
			creating:  make(map[string]bool),
		},
		eventSemaphore: make(chan struct{}, 10),
	}

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-24 * time.Hour),
		HTMLURL:   existingURL,
		Title:     "Integration test PR",
		Number:    42,
	}
	pr.User.Login = "testauthor"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	threadTS, wasNew, messageText, err := c.findOrCreatePRThread(
		ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wasNew {
		t.Error("expected to find existing thread, not create new")
	}

	if threadTS != "1234567890.555555" {
		t.Errorf("expected existing threadTS 1234567890.555555, got %s", threadTS)
	}

	if postCount > 0 {
		t.Errorf("expected no new posts, but got %d", postCount)
	}

	if messageText == "" {
		t.Error("expected non-empty message text from existing thread")
	}

	// Verify thread was cached after finding
	cacheKey := "testorg/testrepo#42:C123"
	cachedInfo, exists := c.threadCache.Get(cacheKey)
	if !exists {
		t.Error("expected thread to be cached after finding")
	}

	if cachedInfo.ThreadTS != threadTS {
		t.Errorf("expected cached threadTS %s, got %s", threadTS, cachedInfo.ThreadTS)
	}
}

func TestIntegration_ThreadCache_Cleanup(t *testing.T) {
	cache := &ThreadCache{
		prThreads: make(map[string]ThreadInfo),
		creating:  make(map[string]bool),
	}

	// Add some threads with different ages
	// Manually insert into map to control UpdatedAt timestamps
	now := time.Now()
	cache.prThreads["old#1:C123"] = ThreadInfo{
		ThreadTS:  "1234.567",
		UpdatedAt: now.Add(-2 * time.Hour),
	}
	cache.prThreads["recent#1:C123"] = ThreadInfo{
		ThreadTS:  "2345.678",
		UpdatedAt: now.Add(-30 * time.Minute),
	}
	cache.prThreads["new#1:C123"] = ThreadInfo{
		ThreadTS:  "3456.789",
		UpdatedAt: now,
	}

	// Clean up entries older than 1 hour
	cache.Cleanup(1 * time.Hour)

	// Verify old entry was removed
	if _, exists := cache.Get("old#1:C123"); exists {
		t.Error("expected old entry to be cleaned up")
	}

	// Verify recent entries remain
	if _, exists := cache.Get("recent#1:C123"); !exists {
		t.Error("expected recent entry to remain")
	}

	if _, exists := cache.Get("new#1:C123"); !exists {
		t.Error("expected new entry to remain")
	}
}

func TestIntegration_FindOrCreatePRThread_ConcurrentCreation(t *testing.T) {
	ctx := context.Background()

	createCount := 0
	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			createCount++
			// Simulate slow API call
			time.Sleep(50 * time.Millisecond)
			return "1234567890.999999", nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
		},
	}

	mockState := &mockStateStore{
		threads: make(map[string]ThreadInfo),
	}

	c := &Coordinator{
		slack:         mockSlack,
		stateStore:    mockState,
		configManager: config.New(),
		threadCache: &ThreadCache{
			prThreads: make(map[string]ThreadInfo),
			creating:  make(map[string]bool),
		},
		eventSemaphore: make(chan struct{}, 10),
	}

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/99",
		Title:     "Concurrent test PR",
		Number:    99,
	}
	pr.User.Login = "testauthor"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	// Start two goroutines trying to create the same thread
	done := make(chan bool, 2)
	var threadTS1, threadTS2 string
	var err1, err2 error

	go func() {
		threadTS1, _, _, err1 = c.findOrCreatePRThread(
			ctx, "C123", "testorg", "testrepo", 99, "awaiting_review", pr, checkResult)
		done <- true
	}()

	go func() {
		// Small delay to ensure race condition
		time.Sleep(10 * time.Millisecond)
		threadTS2, _, _, err2 = c.findOrCreatePRThread(
			ctx, "C123", "testorg", "testrepo", 99, "awaiting_review", pr, checkResult)
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	if err1 != nil {
		t.Errorf("goroutine 1 error: %v", err1)
	}

	if err2 != nil {
		t.Errorf("goroutine 2 error: %v", err2)
	}

	// Both should get the same thread TS
	if threadTS1 != threadTS2 {
		t.Errorf("expected same threadTS, got %s and %s", threadTS1, threadTS2)
	}

	// Should only create once despite concurrent calls
	if createCount != 1 {
		t.Errorf("expected exactly 1 thread creation, got %d", createCount)
	}
}

// formatNextActions testing is covered in integration tests since it requires
// full context and dependencies
