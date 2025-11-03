package bot

import (
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"

	"context"
	"testing"
	"time"

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
