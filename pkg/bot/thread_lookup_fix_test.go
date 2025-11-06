package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// newTestCoordinator creates a coordinator with mocks for testing.
func newTestCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	mockState := &mockStateStore{
		threads: make(map[string]cache.ThreadInfo),
	}
	c := &Coordinator{
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		userMapper:     &mockUserMapper{},
	}
	return c
}

// TestFindPRThread_DatastoreLookup verifies that findPRThread checks the datastore
// when the in-memory cache is empty, preventing duplicate thread creation after restarts.
func TestFindPRThread_DatastoreLookup(t *testing.T) {
	ctx := context.Background()
	c := newTestCoordinator(t)

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
		Title:     "Test PR",
		Number:    42,
	}
	pr.User.Login = "testuser"

	// Save thread to datastore (simulating previous instance or restart)
	threadInfo := cache.ThreadInfo{
		ThreadTS:    "1234567890.123456",
		ChannelID:   "C123456",
		LastState:   "awaiting_review",
		MessageText: ":hourglass: Test PR",
	}
	err := c.stateStore.SaveThread(ctx, "testorg", "testrepo", 42, "C123456", threadInfo)
	if err != nil {
		t.Fatalf("failed to save thread to datastore: %v", err)
	}

	// Clear in-memory cache to simulate restart
	cacheKey := "testorg/testrepo#42:C123456"
	// Note: threadCache doesn't have a Clear() method, but in real scenario it would be empty after restart

	// Search for thread - should find it in datastore
	threadTS, messageText, found := c.findPRThread(ctx, cacheKey, "C123456", "testorg", "testrepo", 42, "awaiting_review", pr)

	if !found {
		t.Fatal("expected to find thread in datastore")
	}

	if threadTS != "1234567890.123456" {
		t.Errorf("threadTS = %q, want %q", threadTS, "1234567890.123456")
	}

	if messageText != ":hourglass: Test PR" {
		t.Errorf("messageText = %q, want %q", messageText, ":hourglass: Test PR")
	}

	// Verify cache was warmed
	cachedInfo, exists := c.threadCache.Get(cacheKey)
	if !exists {
		t.Error("expected datastore result to warm the cache")
	}
	if cachedInfo.ThreadTS != threadTS {
		t.Errorf("cached ThreadTS = %q, want %q", cachedInfo.ThreadTS, threadTS)
	}
}

// TestChannelNameInMessageFormat verifies that ChannelName is correctly passed through
// to message formatting, producing short form "#123" when channel matches repo name.
func TestChannelNameInMessageFormat(t *testing.T) {
	ctx := context.Background()
	c := newTestCoordinator(t)

	// Configure mock Slack to accept PostThread
	//nolint:errcheck // Assigning function to mock field, not calling it
	c.slack.(*mockSlackClient).postThreadFunc = func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
		return "1234567890.123456", nil
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "TESTED_WAITING_FOR_ASSIGNMENT",
			NextAction:    map[string]turn.Action{},
		},
	}

	tests := []struct {
		name            string
		channelName     string
		repo            string
		wantShortForm   bool // true = "#123", false = "repo#123"
		expectedContain string
	}{
		{
			name:            "channel matches repo - use short form",
			channelName:     "testrepo",
			repo:            "testrepo",
			wantShortForm:   true,
			expectedContain: "|#42>",
		},
		{
			name:            "channel differs from repo - use long form",
			channelName:     "general",
			repo:            "testrepo",
			wantShortForm:   false,
			expectedContain: "|testrepo#42>",
		},
		{
			name:            "channel matches repo case-insensitive",
			channelName:     "TestRepo",
			repo:            "testrepo",
			wantShortForm:   true,
			expectedContain: "|#42>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedText string
			//nolint:errcheck // Assigning function to mock field, not calling it
			c.slack.(*mockSlackClient).postThreadFunc = func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
				capturedText = text
				return "1234567890.123456", nil
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
				CreatedAt: time.Now(),
				HTMLURL:   "https://github.com/testorg/" + tt.repo + "/pull/42",
				Title:     "Test PR",
				Number:    42,
			}
			pr.User.Login = "testuser"

			params := threadCreationParams{
				ChannelID:   "C123456",
				ChannelName: tt.channelName,
				Owner:       "testorg",
				Repo:        tt.repo,
				PRNumber:    42,
				PRState:     "awaiting_review",
				PullRequest: pr,
				CheckResult: checkResult,
			}

			_, _, err := c.createPRThread(ctx, params)
			if err != nil {
				t.Fatalf("createPRThread failed: %v", err)
			}

			if !strings.Contains(capturedText, tt.expectedContain) {
				t.Errorf("message text = %q, expected to contain %q", capturedText, tt.expectedContain)
			}
		})
	}
}

// TestThreadCreationParams_PreventsDuplicates verifies that when threads are found
// via datastore lookup, we don't create duplicates.
func TestThreadCreationParams_PreventsDuplicates(t *testing.T) {
	ctx := context.Background()
	c := newTestCoordinator(t)

	var postThreadCallCount int
	//nolint:errcheck // Assigning function to mock field, not calling it
	c.slack.(*mockSlackClient).postThreadFunc = func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
		postThreadCallCount++
		return "1234567890.123456", nil
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "TESTED_WAITING_FOR_ASSIGNMENT",
			NextAction:    map[string]turn.Action{},
		},
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
		Title:     "Test PR",
		Number:    42,
	}
	pr.User.Login = "testuser"

	// First creation - should create thread
	params1 := threadCreationParams{
		ChannelID:   "C123456",
		ChannelName: "testrepo",
		Owner:       "testorg",
		Repo:        "testrepo",
		PRNumber:    42,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	threadTS1, wasCreated1, _, err := c.findOrCreatePRThread(ctx, params1)
	if err != nil {
		t.Fatalf("first findOrCreatePRThread failed: %v", err)
	}

	if !wasCreated1 {
		t.Error("first call should have created thread")
	}

	if postThreadCallCount != 1 {
		t.Errorf("expected 1 PostThread call after first creation, got %d", postThreadCallCount)
	}

	// Clear in-memory cache to simulate restart/new instance
	// In a real scenario, a new instance would have an empty cache but can read from datastore
	c.threadCache = cache.New()

	// Second attempt - should find existing thread in datastore, NOT create new one
	params2 := threadCreationParams{
		ChannelID:   "C123456",
		ChannelName: "testrepo",
		Owner:       "testorg",
		Repo:        "testrepo",
		PRNumber:    42,
		PRState:     "merged",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	threadTS2, wasCreated2, _, err := c.findOrCreatePRThread(ctx, params2)
	if err != nil {
		t.Fatalf("second findOrCreatePRThread failed: %v", err)
	}

	if wasCreated2 {
		t.Error("second call should NOT have created thread (should find in datastore)")
	}

	if threadTS1 != threadTS2 {
		t.Errorf("threadTS mismatch: first=%q, second=%q (should be same thread)", threadTS1, threadTS2)
	}

	if postThreadCallCount != 1 {
		t.Errorf("expected still only 1 PostThread call (no duplicate), got %d", postThreadCallCount)
	}
}
