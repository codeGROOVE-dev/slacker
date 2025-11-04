package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestNewMemoryStore(t *testing.T) {
	store := NewMemoryStore()
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.threads == nil {
		t.Error("expected threads map to be initialized")
	}
	if store.dms == nil {
		t.Error("expected dms map to be initialized")
	}
	if store.dmMessages == nil {
		t.Error("expected dmMessages map to be initialized")
	}
	if store.digests == nil {
		t.Error("expected digests map to be initialized")
	}
	if store.events == nil {
		t.Error("expected events map to be initialized")
	}
	if store.notifications == nil {
		t.Error("expected notifications map to be initialized")
	}
}

func TestThreadOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Test retrieval of non-existent thread
	_, exists := store.Thread(ctx, "owner", "repo", 123, "C123")
	if exists {
		t.Error("expected thread to not exist")
	}

	// Save thread
	threadInfo := ThreadInfo{
		ThreadTS:      "1234567890.123456",
		ChannelID:     "C123",
		LastState:     "awaiting_review",
		MessageText:   "Test PR",
		LastEventTime: time.Now(),
	}

	err := store.SaveThread(ctx, "owner", "repo", 123, "C123", threadInfo)
	if err != nil {
		t.Fatalf("unexpected error saving thread: %v", err)
	}

	// Retrieve saved thread
	retrieved, exists := store.Thread(ctx, "owner", "repo", 123, "C123")
	if !exists {
		t.Fatal("expected thread to exist")
	}

	if retrieved.ThreadTS != threadInfo.ThreadTS {
		t.Errorf("expected ThreadTS %s, got %s", threadInfo.ThreadTS, retrieved.ThreadTS)
	}
	if retrieved.ChannelID != threadInfo.ChannelID {
		t.Errorf("expected ChannelID %s, got %s", threadInfo.ChannelID, retrieved.ChannelID)
	}
	if retrieved.LastState != threadInfo.LastState {
		t.Errorf("expected LastState %s, got %s", threadInfo.LastState, retrieved.LastState)
	}
	if retrieved.MessageText != threadInfo.MessageText {
		t.Errorf("expected MessageText %s, got %s", threadInfo.MessageText, retrieved.MessageText)
	}

	// UpdatedAt should be set automatically
	if retrieved.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestDMOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Test retrieval of non-existent DM
	_, exists := store.LastDM(ctx, "U001", "https://github.com/test/repo/pull/123")
	if exists {
		t.Error("expected DM to not exist")
	}

	// Record DM
	sentAt := time.Now()
	err := store.RecordDM(ctx, "U001", "https://github.com/test/repo/pull/123", sentAt)
	if err != nil {
		t.Fatalf("unexpected error recording DM: %v", err)
	}

	// Retrieve recorded DM
	retrieved, exists := store.LastDM(ctx, "U001", "https://github.com/test/repo/pull/123")
	if !exists {
		t.Fatal("expected DM to exist")
	}

	if !retrieved.Equal(sentAt) {
		t.Errorf("expected sentAt %v, got %v", sentAt, retrieved)
	}
}

func TestDMMessageOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	prURL := "https://github.com/test/repo/pull/123"

	// Test retrieval of non-existent DM message
	_, exists := store.DMMessage(ctx, "U001", prURL)
	if exists {
		t.Error("expected DM message to not exist")
	}

	// Save DM message
	dmInfo := DMInfo{
		SentAt:      time.Now(),
		ChannelID:   "D001",
		MessageTS:   "1234567890.123456",
		MessageText: "Test DM",
	}

	err := store.SaveDMMessage(ctx, "U001", prURL, dmInfo)
	if err != nil {
		t.Fatalf("unexpected error saving DM message: %v", err)
	}

	// Retrieve saved DM message
	retrieved, exists := store.DMMessage(ctx, "U001", prURL)
	if !exists {
		t.Fatal("expected DM message to exist")
	}

	if retrieved.ChannelID != dmInfo.ChannelID {
		t.Errorf("expected ChannelID %s, got %s", dmInfo.ChannelID, retrieved.ChannelID)
	}
	if retrieved.MessageTS != dmInfo.MessageTS {
		t.Errorf("expected MessageTS %s, got %s", dmInfo.MessageTS, retrieved.MessageTS)
	}
	if retrieved.MessageText != dmInfo.MessageText {
		t.Errorf("expected MessageText %s, got %s", dmInfo.MessageText, retrieved.MessageText)
	}

	// UpdatedAt should be set automatically
	if retrieved.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestListDMUsers(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	prURL := "https://github.com/test/repo/pull/123"

	// Initially no users
	users := store.ListDMUsers(ctx, prURL)
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}

	// Save DM messages for multiple users
	dmInfo := DMInfo{
		SentAt:      time.Now(),
		ChannelID:   "D001",
		MessageTS:   "1234567890.123456",
		MessageText: "Test DM",
	}

	if err := store.SaveDMMessage(ctx, "U001", prURL, dmInfo); err != nil {
		t.Fatalf("failed to save DM for U001: %v", err)
	}
	if err := store.SaveDMMessage(ctx, "U002", prURL, dmInfo); err != nil {
		t.Fatalf("failed to save DM for U002: %v", err)
	}
	if err := store.SaveDMMessage(ctx, "U003", prURL, dmInfo); err != nil {
		t.Fatalf("failed to save DM for U003: %v", err)
	}

	// List users
	users = store.ListDMUsers(ctx, prURL)
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}

	// Verify all users are present (order doesn't matter)
	userMap := make(map[string]bool)
	for _, user := range users {
		userMap[user] = true
	}

	for _, expectedUser := range []string{"U001", "U002", "U003"} {
		if !userMap[expectedUser] {
			t.Errorf("expected user %s not found", expectedUser)
		}
	}

	// Different PR should return no users
	users = store.ListDMUsers(ctx, "https://github.com/test/repo/pull/456")
	if len(users) != 0 {
		t.Errorf("expected 0 users for different PR, got %d", len(users))
	}
}

func TestDigestOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Test retrieval of non-existent digest
	_, exists := store.LastDigest(ctx, "U001", "2025-01-15")
	if exists {
		t.Error("expected digest to not exist")
	}

	// Record digest
	sentAt := time.Now()
	err := store.RecordDigest(ctx, "U001", "2025-01-15", sentAt)
	if err != nil {
		t.Fatalf("unexpected error recording digest: %v", err)
	}

	// Retrieve recorded digest
	retrieved, exists := store.LastDigest(ctx, "U001", "2025-01-15")
	if !exists {
		t.Fatal("expected digest to exist")
	}

	if !retrieved.Equal(sentAt) {
		t.Errorf("expected sentAt %v, got %v", sentAt, retrieved)
	}
}

func TestEventProcessing(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	eventKey := "event-123"

	// Event should not be processed initially
	if store.WasProcessed(ctx, eventKey) {
		t.Error("expected event to not be processed")
	}

	// Mark event as processed
	err := store.MarkProcessed(ctx, eventKey, 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error marking event: %v", err)
	}

	// Event should now be processed
	if !store.WasProcessed(ctx, eventKey) {
		t.Error("expected event to be processed")
	}
}

func TestNotificationOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	prURL := "https://github.com/test/repo/pull/123"

	// Last notification should be zero time initially
	lastNotif := store.LastNotification(ctx, prURL)
	if !lastNotif.IsZero() {
		t.Error("expected zero time for non-existent notification")
	}

	// Record notification
	notifiedAt := time.Now()
	err := store.RecordNotification(ctx, prURL, notifiedAt)
	if err != nil {
		t.Fatalf("unexpected error recording notification: %v", err)
	}

	// Retrieve last notification
	retrieved := store.LastNotification(ctx, prURL)
	if !retrieved.Equal(notifiedAt) {
		t.Errorf("expected notifiedAt %v, got %v", notifiedAt, retrieved)
	}
}

func TestCleanup(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Add some old data
	oldTime := time.Now().Add(-100 * 24 * time.Hour)

	// Old thread (>30 days)
	threadInfo := ThreadInfo{
		UpdatedAt: oldTime,
		ThreadTS:  "old-thread",
	}
	store.threads[threadKey("owner", "repo", 1, "C123")] = threadInfo

	// Old DM (>90 days)
	store.dms[dmKey("U001", "pr-url-1")] = oldTime

	// Old DM message (>90 days)
	dmInfo := DMInfo{
		UpdatedAt: oldTime,
	}
	store.dmMessages[dmKey("U001", "pr-url-2")] = dmInfo

	// Old digest (>30 days)
	store.digests[digestKey("U001", "2024-01-01")] = oldTime

	// Old event (>24 hours)
	store.events["old-event"] = oldTime

	// Add some recent data that shouldn't be cleaned up
	recentTime := time.Now()
	store.threads[threadKey("owner", "repo", 2, "C456")] = ThreadInfo{UpdatedAt: recentTime}
	store.dms[dmKey("U002", "pr-url-3")] = recentTime
	store.dmMessages[dmKey("U002", "pr-url-4")] = DMInfo{UpdatedAt: recentTime}
	store.digests[digestKey("U002", "2025-01-15")] = recentTime
	store.events["recent-event"] = recentTime

	// Run cleanup
	err := store.Cleanup(ctx)
	if err != nil {
		t.Fatalf("unexpected error during cleanup: %v", err)
	}

	// Verify old data was cleaned up
	if len(store.threads) != 1 {
		t.Errorf("expected 1 thread after cleanup, got %d", len(store.threads))
	}
	if len(store.dms) != 1 {
		t.Errorf("expected 1 DM after cleanup, got %d", len(store.dms))
	}
	if len(store.dmMessages) != 1 {
		t.Errorf("expected 1 DM message after cleanup, got %d", len(store.dmMessages))
	}
	if len(store.digests) != 1 {
		t.Errorf("expected 1 digest after cleanup, got %d", len(store.digests))
	}
	if len(store.events) != 1 {
		t.Errorf("expected 1 event after cleanup, got %d", len(store.events))
	}

	// Verify recent data still exists
	if _, exists := store.threads[threadKey("owner", "repo", 2, "C456")]; !exists {
		t.Error("expected recent thread to still exist")
	}
}

func TestClose(t *testing.T) {
	store := NewMemoryStore()

	// Close should not error
	err := store.Close()
	if err != nil {
		t.Errorf("unexpected error closing store: %v", err)
	}
}

func TestPendingDMOperations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Test retrieval when no pending DMs exist
	pending, err := store.PendingDMs(ctx, time.Now())
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending DMs, got %d", len(pending))
	}

	// Queue a DM that should be sent now
	now := time.Now()
	dm1 := PendingDM{
		ID:            "dm-001",
		WorkspaceID:   "T123",
		UserID:        "U001",
		PROwner:       "test-org",
		PRRepo:        "test-repo",
		PRNumber:      123,
		PRURL:         "https://github.com/test-org/test-repo/pull/123",
		PRTitle:       "Test PR",
		PRAuthor:      "author",
		PRState:       "open",
		WorkflowState: "awaiting_review",
		NextActions:   `{"U001":{"kind":"review"}}`,
		ChannelID:     "C123",
		ChannelName:   "test-channel",
		QueuedAt:      now.Add(-10 * time.Minute),
		SendAfter:     now.Add(-5 * time.Minute), // 5 minutes ago - ready to send
	}

	err = store.QueuePendingDM(ctx, &dm1)
	if err != nil {
		t.Fatalf("unexpected error queueing DM: %v", err)
	}

	// Queue a DM that should be sent in the future
	dm2 := PendingDM{
		ID:            "dm-002",
		WorkspaceID:   "T123",
		UserID:        "U002",
		PROwner:       "test-org",
		PRRepo:        "test-repo",
		PRNumber:      456,
		PRURL:         "https://github.com/test-org/test-repo/pull/456",
		PRTitle:       "Another PR",
		PRAuthor:      "author2",
		PRState:       "open",
		WorkflowState: "tests_broken",
		NextActions:   `{"U002":{"kind":"fix"}}`,
		ChannelID:     "C456",
		ChannelName:   "another-channel",
		QueuedAt:      now,
		SendAfter:     now.Add(10 * time.Minute), // 10 minutes from now - not ready yet
	}

	err = store.QueuePendingDM(ctx, &dm2)
	if err != nil {
		t.Fatalf("unexpected error queueing second DM: %v", err)
	}

	// Get pending DMs that are ready to send
	pending, err = store.PendingDMs(ctx, now)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs: %v", err)
	}

	// Only dm1 should be returned (dm2 is in the future)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending DM, got %d", len(pending))
	}

	if pending[0].ID != "dm-001" {
		t.Errorf("expected DM ID dm-001, got %s", pending[0].ID)
	}
	if pending[0].UserID != "U001" {
		t.Errorf("expected UserID U001, got %s", pending[0].UserID)
	}
	if pending[0].PRNumber != 123 {
		t.Errorf("expected PRNumber 123, got %d", pending[0].PRNumber)
	}

	// Get pending DMs 15 minutes from now - both should be ready
	future := now.Add(15 * time.Minute)
	pending, err = store.PendingDMs(ctx, future)
	if err != nil {
		t.Fatalf("unexpected error getting future pending DMs: %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("expected 2 pending DMs in future, got %d", len(pending))
	}

	// Remove one DM
	err = store.RemovePendingDM(ctx, "dm-001")
	if err != nil {
		t.Fatalf("unexpected error removing DM: %v", err)
	}

	// Now only dm2 should remain
	pending, err = store.PendingDMs(ctx, future)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs after removal: %v", err)
	}

	if len(pending) != 1 {
		t.Fatalf("expected 1 pending DM after removal, got %d", len(pending))
	}

	if pending[0].ID != "dm-002" {
		t.Errorf("expected remaining DM to be dm-002, got %s", pending[0].ID)
	}

	// Remove non-existent DM should not error
	err = store.RemovePendingDM(ctx, "dm-999")
	if err != nil {
		t.Errorf("unexpected error removing non-existent DM: %v", err)
	}
}

func TestPendingDMCleanup(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	now := time.Now()
	oldTime := now.Add(-100 * 24 * time.Hour) // 100 days ago

	// Add an old pending DM (>90 days)
	oldDM := PendingDM{
		ID:        "old-dm",
		UserID:    "U001",
		PRURL:     "https://github.com/test/repo/pull/1",
		QueuedAt:  oldTime,
		SendAfter: oldTime,
	}
	if err := store.QueuePendingDM(ctx, &oldDM); err != nil {
		t.Fatalf("failed to queue old DM: %v", err)
	}

	// Add a recent pending DM
	recentDM := PendingDM{
		ID:        "recent-dm",
		UserID:    "U002",
		PRURL:     "https://github.com/test/repo/pull/2",
		QueuedAt:  now,
		SendAfter: now.Add(10 * time.Minute),
	}
	if err := store.QueuePendingDM(ctx, &recentDM); err != nil {
		t.Fatalf("failed to queue recent DM: %v", err)
	}

	// Run cleanup
	err := store.Cleanup(ctx)
	if err != nil {
		t.Fatalf("unexpected error during cleanup: %v", err)
	}

	// Verify old DM was removed
	pending, err := store.PendingDMs(ctx, now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs: %v", err)
	}

	if len(pending) != 1 {
		t.Fatalf("expected 1 pending DM after cleanup, got %d", len(pending))
	}

	if pending[0].ID != "recent-dm" {
		t.Errorf("expected recent-dm to remain, got %s", pending[0].ID)
	}
}

func TestPendingDMConcurrency(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	now := time.Now()

	// Queue multiple DMs concurrently
	done := make(chan bool, 3)

	for i := range 3 {
		go func(index int) {
			dm := PendingDM{
				ID:        fmt.Sprintf("dm-%d", index),
				UserID:    fmt.Sprintf("U%03d", index),
				PRURL:     fmt.Sprintf("https://github.com/test/repo/pull/%d", index),
				QueuedAt:  now,
				SendAfter: now.Add(-1 * time.Minute),
			}
			if err := store.QueuePendingDM(ctx, &dm); err != nil {
				t.Errorf("failed to queue DM in goroutine %d: %v", index, err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for range 3 {
		<-done
	}

	// Get all pending DMs
	pending, err := store.PendingDMs(ctx, now)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs: %v", err)
	}

	if len(pending) != 3 {
		t.Fatalf("expected 3 pending DMs, got %d", len(pending))
	}

	// Remove DMs concurrently
	for i := range 3 {
		go func(index int) {
			if err := store.RemovePendingDM(ctx, fmt.Sprintf("dm-%d", index)); err != nil {
				t.Errorf("failed to remove DM in goroutine %d: %v", index, err)
			}
			done <- true
		}(i)
	}

	// Wait for all removals
	for range 3 {
		<-done
	}

	// Verify all removed
	pending, err = store.PendingDMs(ctx, now)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs after removal: %v", err)
	}

	if len(pending) != 0 {
		t.Errorf("expected 0 pending DMs after concurrent removal, got %d", len(pending))
	}
}
