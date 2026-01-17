package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
)

func TestCancelPendingDMs_Success(t *testing.T) {
	ctx := context.Background()

	// Create test state store
	testStore := state.NewMemoryStore()

	// Add a pending DM
	userID := "U123"
	prURL := "https://github.com/org/repo/pull/1"

	dm := &state.PendingDM{
		ID:        "dm-123",
		UserID:    userID,
		PRURL:     prURL,
		SendAfter: time.Now().Add(1 * time.Hour),
		QueuedAt:  time.Now(),
		PRTitle:   "Test PR",
		PROwner:   "org",
		PRRepo:    "repo",
	}

	err := testStore.QueuePendingDM(ctx, dm)
	if err != nil {
		t.Fatalf("failed to queue pending DM: %v", err)
	}

	// Create coordinator
	c := &Coordinator{
		stateStore:  testStore,
		threadCache: cache.New(),
	}

	// Cancel the pending DM
	c.cancelPendingDMs(ctx, userID, prURL)

	// Verify the DM was removed
	pendingDMs, err := testStore.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("failed to get pending DMs: %v", err)
	}

	// Should be zero pending DMs after cancellation
	if len(pendingDMs) != 0 {
		t.Errorf("expected 0 pending DMs after cancellation, got %d", len(pendingDMs))
	}
}

func TestCancelPendingDMs_NoMatchingDM(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()

	// Add a pending DM for a different PR
	dm := &state.PendingDM{
		ID:        "dm-123",
		UserID:    "U123",
		PRURL:     "https://github.com/org/repo/pull/999",
		SendAfter: time.Now().Add(1 * time.Hour),
		QueuedAt:  time.Now(),
		PRTitle:   "Test PR",
		PROwner:   "org",
		PRRepo:    "repo",
	}
	err := testStore.QueuePendingDM(ctx, dm)
	if err != nil {
		t.Fatalf("failed to queue pending DM: %v", err)
	}

	c := &Coordinator{
		stateStore:  testStore,
		threadCache: cache.New(),
	}

	// Try to cancel a non-existent DM (different PR)
	c.cancelPendingDMs(ctx, "U123", "https://github.com/org/repo/pull/1")

	// Original DM should still be there
	pendingDMs, err := testStore.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("failed to get pending DMs: %v", err)
	}

	if len(pendingDMs) != 1 {
		t.Errorf("expected 1 pending DM to remain, got %d", len(pendingDMs))
	}
}

func TestCancelPendingDMs_MultipleMatching(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	userID := "U123"
	prURL := "https://github.com/org/repo/pull/1"

	// Add multiple pending DMs for same user+PR
	dm1 := &state.PendingDM{
		ID:        "dm-1",
		UserID:    userID,
		PRURL:     prURL,
		SendAfter: time.Now().Add(1 * time.Hour),
		QueuedAt:  time.Now(),
		PRTitle:   "Test PR 1",
		PROwner:   "org",
		PRRepo:    "repo",
	}
	err := testStore.QueuePendingDM(ctx, dm1)
	if err != nil {
		t.Fatalf("failed to queue pending DM 1: %v", err)
	}

	dm2 := &state.PendingDM{
		ID:        "dm-2",
		UserID:    userID,
		PRURL:     prURL,
		SendAfter: time.Now().Add(2 * time.Hour),
		QueuedAt:  time.Now(),
		PRTitle:   "Test PR 1",
		PROwner:   "org",
		PRRepo:    "repo",
	}
	err = testStore.QueuePendingDM(ctx, dm2)
	if err != nil {
		t.Fatalf("failed to queue pending DM 2: %v", err)
	}

	// Add one for different user/PR
	dm3 := &state.PendingDM{
		ID:        "dm-3",
		UserID:    "U999",
		PRURL:     "https://github.com/org/repo/pull/2",
		SendAfter: time.Now().Add(1 * time.Hour),
		QueuedAt:  time.Now(),
		PRTitle:   "Test PR 2",
		PROwner:   "org",
		PRRepo:    "repo",
	}
	err = testStore.QueuePendingDM(ctx, dm3)
	if err != nil {
		t.Fatalf("failed to queue pending DM 3: %v", err)
	}

	c := &Coordinator{
		stateStore:  testStore,
		threadCache: cache.New(),
	}

	// Cancel all DMs for this user+PR
	c.cancelPendingDMs(ctx, userID, prURL)

	// Should only have the unrelated DM left
	pendingDMs, err := testStore.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("failed to get pending DMs: %v", err)
	}

	if len(pendingDMs) != 1 {
		t.Errorf("expected 1 unrelated pending DM to remain, got %d", len(pendingDMs))
	}

	if len(pendingDMs) > 0 && pendingDMs[0].UserID == userID {
		t.Error("cancelled user's DMs should not remain")
	}
}

func TestCancelPendingDMs_ErrorGettingPendingDMs(t *testing.T) {
	ctx := context.Background()

	// Create state store that returns error for PendingDMs
	testStore := NewMockState().
		WithPendingDMsError(errors.New("database error")).
		Build()

	c := &Coordinator{
		stateStore:  testStore,
		threadCache: cache.New(),
	}

	// Should handle error gracefully (logs warning but doesn't panic)
	c.cancelPendingDMs(ctx, "U123", "https://github.com/org/repo/pull/1")

	// Test passes if no panic occurs
}

func TestCancelPendingDMs_ErrorRemovingDM(t *testing.T) {
	ctx := context.Background()

	userID := "U123"
	prURL := "https://github.com/org/repo/pull/1"

	// Create state store with a pending DM
	testStore := NewMockState().
		WithRemovePendingDMError(errors.New("database error")).
		Build()

	// Manually add a pending DM to the store
	dm := &state.PendingDM{
		ID:        "dm-123",
		UserID:    userID,
		PRURL:     prURL,
		SendAfter: time.Now().Add(1 * time.Hour),
		QueuedAt:  time.Now(),
		PRTitle:   "Test PR",
		PROwner:   "org",
		PRRepo:    "repo",
	}
	_ = testStore.QueuePendingDM(ctx, dm)

	c := &Coordinator{
		stateStore:  testStore,
		threadCache: cache.New(),
	}

	// Should handle error gracefully (logs warning but doesn't panic)
	c.cancelPendingDMs(ctx, userID, prURL)

	// Test passes if no panic occurs
}
