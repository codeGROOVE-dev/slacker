package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestWaitForConcurrentCreation_ThreadFoundDuringWait tests finding thread in cache during wait
func TestWaitForConcurrentCreation_ThreadFoundDuringWait(t *testing.T) {
	threadCache := cache.New()
	cacheKey := "org/repo#1:C123"

	c := &Coordinator{
		threadCache: threadCache,
	}

	// Mark as creating to simulate another goroutine creating
	if !threadCache.MarkCreating(cacheKey) {
		t.Fatal("failed to mark as creating")
	}

	// Start a goroutine to populate cache after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		threadCache.Set(cacheKey, cache.ThreadInfo{
			ThreadTS:    "found.thread",
			MessageText: "found message",
		})
		threadCache.UnmarkCreating(cacheKey)
	}()

	// Wait for concurrent creation
	threadTS, messageText, shouldProceed := c.waitForConcurrentCreation(cacheKey)

	if shouldProceed {
		t.Error("expected shouldProceed to be false when thread found")
	}

	if threadTS != "found.thread" {
		t.Errorf("expected threadTS 'found.thread', got %s", threadTS)
	}

	if messageText != "found message" {
		t.Errorf("expected messageText 'found message', got %s", messageText)
	}
}

// TestWaitForConcurrentCreation_OtherGoroutineFinishedWithoutCaching tests when other goroutine finishes but doesn't cache
func TestWaitForConcurrentCreation_OtherGoroutineFinishedWithoutCaching(t *testing.T) {
	threadCache := cache.New()
	cacheKey := "org/repo#1:C123"

	c := &Coordinator{
		threadCache: threadCache,
	}

	// Mark as creating to simulate another goroutine creating
	if !threadCache.MarkCreating(cacheKey) {
		t.Fatal("failed to mark as creating")
	}

	// Start a goroutine to unmark without caching (simulating failure)
	go func() {
		time.Sleep(200 * time.Millisecond)
		threadCache.UnmarkCreating(cacheKey)
	}()

	// Wait for concurrent creation
	threadTS, messageText, shouldProceed := c.waitForConcurrentCreation(cacheKey)

	if !shouldProceed {
		t.Error("expected shouldProceed to be true when other goroutine finished without caching")
	}

	if threadTS != "" || messageText != "" {
		t.Errorf("expected empty strings, got threadTS=%s, messageText=%s", threadTS, messageText)
	}

	// Verify we successfully marked as creating
	if !threadCache.IsCreating(cacheKey) {
		t.Error("expected to have successfully marked as creating")
	}
	threadCache.UnmarkCreating(cacheKey) // Clean up
}

// Note: Timeout scenarios (30+ seconds) are difficult to test in unit tests and are
// covered by integration tests or left as untested defensive code.

// TestCreatePRThreadWithLocking_ConcurrentCreation tests the full concurrent creation flow
func TestCreatePRThreadWithLocking_ConcurrentCreation(t *testing.T) {
	ctx := context.Background()

	var postThreadCalls int
	var mu sync.Mutex

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			postThreadCalls++
			// Add delay to simulate real API call
			time.Sleep(100 * time.Millisecond)
			return "1234.5678", nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	threadCache := cache.New()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    &mockUserMapper{mapping: map[string]string{}},
		stateStore:    state.NewMemoryStore(),
		threadCache:   threadCache,
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
		HTMLURL:   "https://github.com/org/repo/pull/1",
		Title:     "Test PR",
		Number:    1,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Merged: false,
			Draft:  false,
		},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction:    map[string]turn.Action{},
		},
	}

	params := threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	// Launch two concurrent thread creation attempts
	var wg sync.WaitGroup
	var info1, info2 cache.ThreadInfo
	var wasCreated1, wasCreated2 bool
	var err1, err2 error

	wg.Add(2)

	go func() {
		defer wg.Done()
		info1, wasCreated1, err1 = c.createPRThreadWithLocking(ctx, params)
	}()

	go func() {
		defer wg.Done()
		// Add small delay to ensure second goroutine hits the concurrent creation path
		time.Sleep(10 * time.Millisecond)
		info2, wasCreated2, err2 = c.createPRThreadWithLocking(ctx, params)
	}()

	wg.Wait()

	// Check results
	if err1 != nil {
		t.Errorf("goroutine 1 error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("goroutine 2 error: %v", err2)
	}

	// Exactly one should have created the thread
	createdCount := 0
	if wasCreated1 {
		createdCount++
	}
	if wasCreated2 {
		createdCount++
	}

	if createdCount != 1 {
		t.Errorf("expected exactly 1 goroutine to create thread, got %d", createdCount)
	}

	// Both should have the same threadTS
	if info1.ThreadTS != info2.ThreadTS {
		t.Errorf("expected same threadTS, got %s and %s", info1.ThreadTS, info2.ThreadTS)
	}

	// PostThread should only be called once
	mu.Lock()
	if postThreadCalls != 1 {
		t.Errorf("expected PostThread to be called once, got %d calls", postThreadCalls)
	}
	mu.Unlock()
}

// TestCreatePRThreadWithLocking_WaitThenProceed tests when waiting goroutine proceeds after other finishes without caching
func TestCreatePRThreadWithLocking_WaitThenProceed(t *testing.T) {
	ctx := context.Background()

	var postThreadCalls int
	var mu sync.Mutex

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			postThreadCalls++
			return "1234.5678", nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	threadCache := cache.New()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    &mockUserMapper{mapping: map[string]string{}},
		stateStore:    state.NewMemoryStore(),
		threadCache:   threadCache,
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
		HTMLURL:   "https://github.com/org/repo/pull/1",
		Title:     "Test PR",
		Number:    1,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Merged: false,
			Draft:  false,
		},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction:    map[string]turn.Action{},
		},
	}

	params := threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	cacheKey := "org/repo#1:C123"

	// Pre-mark as creating to simulate another goroutine
	if !threadCache.MarkCreating(cacheKey) {
		t.Fatal("failed to mark as creating")
	}

	// Start goroutine that will unmark after delay (simulating failure without caching)
	go func() {
		time.Sleep(300 * time.Millisecond)
		threadCache.UnmarkCreating(cacheKey)
	}()

	// This should wait, then proceed to create
	info, wasCreated, err := c.createPRThreadWithLocking(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wasCreated {
		t.Error("expected wasCreated to be true when proceeding after wait")
	}

	if info.ThreadTS != "1234.5678" {
		t.Errorf("expected threadTS 1234.5678, got %s", info.ThreadTS)
	}

	mu.Lock()
	if postThreadCalls != 1 {
		t.Errorf("expected PostThread to be called once, got %d calls", postThreadCalls)
	}
	mu.Unlock()
}
