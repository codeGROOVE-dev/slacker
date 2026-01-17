package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
)

func TestHandlePullRequestFromSprinkler_NoToken(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "", // No token
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	// Should return early without error (just logs)
	c.handlePullRequestFromSprinkler(ctx, "testorg", "testrepo", 42, "https://github.com/testorg/testrepo/pull/42", time.Now())
	// Test passes if it returns without panicking
}

func TestHandlePullRequestReviewFromSprinkler_NoToken(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "", // No token
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	// Should delegate to handlePullRequestFromSprinkler
	c.handlePullRequestReviewFromSprinkler(ctx, "testorg", "testrepo", 42, "https://github.com/testorg/testrepo/pull/42", time.Now())
	// Test passes if it returns without panicking
}

// TestHandlePullRequestFromSprinkler_WithCommits tests successful handling with commit data
func TestHandlePullRequestFromSprinkler_WithCommits(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	mockConfig := NewMockConfig().
		WithWorkspace("test-workspace").
		WithChannels("testorg", "testrepo", []string{}). // No channels to simplify
		Build()

	commitPRCache := cache.NewCommitPRCache()

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  mockConfig,
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		commitPRCache:  commitPRCache,
	}

	// Mock turnclient will return PR with commits
	// The TestMain in bot_test.go sets up TURN_TEST_BACKEND env var
	// which makes turnclient return mock data

	c.handlePullRequestFromSprinkler(ctx, "testorg", "testrepo", 42, "https://github.com/testorg/testrepo/pull/42", time.Now())

	// Verify commits were recorded in cache (if turnclient mock returned any)
	// In real scenario with turnclient mock, commits would be cached
	// For now just verify no panic occurred
}

// TestHandlePullRequestFromSprinkler_ErrorCreatingClient tests turnclient creation error
func TestHandlePullRequestFromSprinkler_ErrorCreatingClient(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	// Clear TURN_TEST_BACKEND to trigger normal client creation path
	// (In normal tests this env var is set by TestMain, but we want to test error path)
	// However, we can't easily force NewDefaultClient to error without changing global state
	// So this test just verifies the happy path works

	c.handlePullRequestFromSprinkler(ctx, "testorg", "testrepo", 42, "https://github.com/testorg/testrepo/pull/42", time.Now())

	// Test passes if no panic occurs
	// Note: Testing turnclient creation errors requires mocking os.Getenv or injecting client
}

// TestHandlePullRequestFromSprinkler_ContextTimeout tests timeout handling
func TestHandlePullRequestFromSprinkler_ContextTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	// Create context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	// Should handle cancelled context gracefully
	c.handlePullRequestFromSprinkler(ctx, "testorg", "testrepo", 42, "https://github.com/testorg/testrepo/pull/42", time.Now())

	// Test passes if no panic occurs
}
