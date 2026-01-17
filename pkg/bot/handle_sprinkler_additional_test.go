package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
)

// TestHandleSprinklerEvent_NonCheckEventWithoutPRNumber tests non-check events without PR number
func TestHandleSprinklerEvent_NonCheckEventWithoutPRNumber(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	c := &Coordinator{
		github:            &mockGitHub{org: "testorg", token: "test-token"},
		slack:             &mockSlackClient{},
		stateStore:        mockState,
		configManager:     NewMockConfig().Build(),
		threadCache:       cache.New(),
		eventSemaphore:    make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "push", // Non-check event
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/commit/abc123", // No PR number
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	// Should handle non-check event without PR number gracefully
	c.handleSprinklerEvent(ctx, event, "testorg")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Test passes if no panic occurs
}

// TestHandleSprinklerEvent_CheckEventMultiplePRs tests check event finding multiple PRs
func TestHandleSprinklerEvent_CheckEventMultiplePRs(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
		findPRsFunc: func(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
			if commitSHA == "abc123" {
				return []int{42, 43, 44}, nil // Multiple PRs
			}
			return []int{}, nil
		},
	}

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{}).
		Build()

	c := &Coordinator{
		github:            mockGH,
		slack:             &mockSlackClient{},
		stateStore:        mockState,
		configManager:     mockConfig,
		threadCache:       cache.New(),
		commitPRCache:     cache.NewCommitPRCache(),
		eventSemaphore:    make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "check_run",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/commit/abc123", // No PR number in URL
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	// Should find multiple PRs and process each
	c.handleSprinklerEvent(ctx, event, "testorg")

	// Wait for async processing
	time.Sleep(200 * time.Millisecond)

	// Test passes if no panic occurs (processes 3 PRs)
}

// TestHandleSprinklerEvent_ProcessEventError tests when processEvent returns error
func TestHandleSprinklerEvent_ProcessEventError(t *testing.T) {
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

	// Config that will cause processEvent to fail (no workspace)
	mockConfig := NewMockConfig().Build()

	c := &Coordinator{
		github:            mockGH,
		slack:             &mockSlackClient{},
		stateStore:        mockState,
		configManager:     mockConfig,
		threadCache:       cache.New(),
		commitPRCache:     cache.NewCommitPRCache(),
		eventSemaphore:    make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "check_suite",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	// Should handle processEvent errors gracefully
	c.handleSprinklerEvent(ctx, event, "testorg")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Test passes if no panic occurs
}

// TestHandleSprinklerEvent_StateStoreError tests database error (not ErrAlreadyProcessed)
func TestHandleSprinklerEvent_StateStoreError(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		markProcessedErr: errors.New("database connection failed"),
	}

	c := &Coordinator{
		github:            &mockGitHub{org: "testorg", token: "test-token"},
		slack:             &mockSlackClient{},
		stateStore:        mockState,
		configManager:     NewMockConfig().Build(),
		threadCache:       cache.New(),
		eventSemaphore:    make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Raw: map[string]interface{}{
			"delivery_id": "12345",
		},
	}

	// Should handle state store errors gracefully
	c.handleSprinklerEvent(ctx, event, "testorg")

	// Event should not be processed due to database error
	time.Sleep(50 * time.Millisecond)

	// Test passes if no panic occurs
}

// TestHandleSprinklerEvent_InvalidURLFormat tests URL parsing error
func TestHandleSprinklerEvent_InvalidURLFormat(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	c := &Coordinator{
		github:            &mockGitHub{org: "testorg", token: "test-token"},
		slack:             &mockSlackClient{},
		stateStore:        mockState,
		configManager:     NewMockConfig().Build(),
		threadCache:       cache.New(),
		eventSemaphore:    make(chan struct{}, 10),
	}

	event := client.Event{
		Type:      "pull_request",
		Timestamp: time.Now(),
		URL:       "https://invalid.com/foo", // Too few parts
		Raw: map[string]interface{}{
			"delivery_id": "12345",
		},
	}

	// Should handle invalid URL format gracefully
	c.handleSprinklerEvent(ctx, event, "testorg")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Test passes if no panic occurs
}
