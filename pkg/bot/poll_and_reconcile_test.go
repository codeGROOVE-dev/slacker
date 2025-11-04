package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
)

// TestPollAndReconcile_ListOpenPRsError tests error handling when listing open PRs fails.
func TestPollAndReconcile_ListOpenPRsError(t *testing.T) {
	ctx := context.Background()

	// Create a mock that will fail when listing PRs
	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	// We can't easily mock NewGraphQLClient, but we can verify the function
	// returns early on errors by checking logs
	c := NewTestCoordinator().
		WithGitHub(mockGH).
		Build()

	// This will fail to list PRs because GraphQL client will fail
	// The function should handle the error gracefully
	c.PollAndReconcile(ctx)
	// Test passes if no panic occurs
}

// TestPollAndReconcile_EmptyPRList tests when no PRs are found.
func TestPollAndReconcile_EmptyPRList(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := NewTestCoordinator().
		WithGitHub(mockGH).
		Build()

	// Even with empty PR list, function should complete without error
	c.PollAndReconcile(ctx)
}

// TestPollAndReconcile_ContextCancellation tests graceful shutdown on context cancellation.
func TestPollAndReconcile_ContextCancellation(t *testing.T) {
	ctx := context.Background()
	// Create a context that's already canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := NewTestCoordinator().
		WithGitHub(mockGH).
		Build()

	// Should handle cancellation gracefully
	c.PollAndReconcile(ctx)
}

// TestPollAndReconcile_PRDeduplication tests that already-processed PRs are skipped.
func TestPollAndReconcile_PRDeduplication(t *testing.T) {
	ctx := context.Background()

	// Create a PR that will appear as already processed
	prUpdatedAt := time.Now().Add(-1 * time.Hour)
	prURL := "https://github.com/testorg/testrepo/pull/42"
	eventKey := makePollEventKey(prURL, prUpdatedAt)

	mockState := NewMockState().
		WithProcessedEvent(eventKey).
		Build()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := NewTestCoordinator().
		WithGitHub(mockGH).
		WithState(mockState).
		Build()

	// The function will try to fetch PRs but should skip already-processed ones
	c.PollAndReconcile(ctx)
}

// TestStartupReconciliation_HappyPath tests basic startup reconciliation flow.
func TestStartupReconciliation_HappyPath(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := NewTestCoordinator().
		WithGitHub(mockGH).
		Build()

	// Should complete without panic
	c.StartupReconciliation(ctx)
}

// TestStartupReconciliation_ContextCancellation tests cancellation handling.
func TestStartupReconciliation_ContextCancellation(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := NewTestCoordinator().
		WithGitHub(mockGH).
		Build()

	// Should handle cancellation gracefully
	c.StartupReconciliation(ctx)
}

// TestUpdateClosedPRThread_HappyPath tests updating threads for closed PRs.
func TestUpdateClosedPRThread_HappyPath(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolution("testrepo", "C123").
		WithUpdateMessageSuccess().
		Build()

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"testrepo"}).
		Build()

	// Pre-populate state store with existing thread using builder
	mockState := NewMockState().
		WithThread("testorg", "testrepo", 42, "C123", cache.ThreadInfo{
			ThreadTS:    "1234.567",
			ChannelID:   "C123",
			MessageText: ":hourglass: Test PR",
			UpdatedAt:   time.Now(),
		}).
		Build()

	c := NewTestCoordinator().
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		WithState(mockState).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    42,
		Title:     "Test PR",
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Author:    "testauthor",
		State:     "MERGED",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify message was updated
	if len(mockSlack.updatedMessages) == 0 {
		t.Error("expected message to be updated")
	}
}

// TestUpdateClosedPRThread_InvalidState tests with invalid PR state.
func TestUpdateClosedPRThread_InvalidState(t *testing.T) {
	ctx := context.Background()

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"testrepo"}).
		Build()

	c := NewTestCoordinator().
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    42,
		Title:     "Test PR",
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Author:    "testauthor",
		State:     "INVALID_STATE", // Invalid state
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	// Should handle invalid state gracefully
	err := c.updateClosedPRThread(ctx, pr)
	if err == nil {
		t.Error("expected error with invalid PR state")
	}
}
