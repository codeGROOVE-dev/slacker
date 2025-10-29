package state

import (
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
	store := NewMemoryStore()

	// Test retrieval of non-existent thread
	_, exists := store.Thread("owner", "repo", 123, "C123")
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

	err := store.SaveThread("owner", "repo", 123, "C123", threadInfo)
	if err != nil {
		t.Fatalf("unexpected error saving thread: %v", err)
	}

	// Retrieve saved thread
	retrieved, exists := store.Thread("owner", "repo", 123, "C123")
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
	store := NewMemoryStore()

	// Test retrieval of non-existent DM
	_, exists := store.LastDM("U001", "https://github.com/test/repo/pull/123")
	if exists {
		t.Error("expected DM to not exist")
	}

	// Record DM
	sentAt := time.Now()
	err := store.RecordDM("U001", "https://github.com/test/repo/pull/123", sentAt)
	if err != nil {
		t.Fatalf("unexpected error recording DM: %v", err)
	}

	// Retrieve recorded DM
	retrieved, exists := store.LastDM("U001", "https://github.com/test/repo/pull/123")
	if !exists {
		t.Fatal("expected DM to exist")
	}

	if !retrieved.Equal(sentAt) {
		t.Errorf("expected sentAt %v, got %v", sentAt, retrieved)
	}
}

func TestDMMessageOperations(t *testing.T) {
	store := NewMemoryStore()

	prURL := "https://github.com/test/repo/pull/123"

	// Test retrieval of non-existent DM message
	_, exists := store.DMMessage("U001", prURL)
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

	err := store.SaveDMMessage("U001", prURL, dmInfo)
	if err != nil {
		t.Fatalf("unexpected error saving DM message: %v", err)
	}

	// Retrieve saved DM message
	retrieved, exists := store.DMMessage("U001", prURL)
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
	store := NewMemoryStore()

	prURL := "https://github.com/test/repo/pull/123"

	// Initially no users
	users := store.ListDMUsers(prURL)
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

	store.SaveDMMessage("U001", prURL, dmInfo)
	store.SaveDMMessage("U002", prURL, dmInfo)
	store.SaveDMMessage("U003", prURL, dmInfo)

	// List users
	users = store.ListDMUsers(prURL)
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
	users = store.ListDMUsers("https://github.com/test/repo/pull/456")
	if len(users) != 0 {
		t.Errorf("expected 0 users for different PR, got %d", len(users))
	}
}

func TestDigestOperations(t *testing.T) {
	store := NewMemoryStore()

	// Test retrieval of non-existent digest
	_, exists := store.LastDigest("U001", "2025-01-15")
	if exists {
		t.Error("expected digest to not exist")
	}

	// Record digest
	sentAt := time.Now()
	err := store.RecordDigest("U001", "2025-01-15", sentAt)
	if err != nil {
		t.Fatalf("unexpected error recording digest: %v", err)
	}

	// Retrieve recorded digest
	retrieved, exists := store.LastDigest("U001", "2025-01-15")
	if !exists {
		t.Fatal("expected digest to exist")
	}

	if !retrieved.Equal(sentAt) {
		t.Errorf("expected sentAt %v, got %v", sentAt, retrieved)
	}
}

func TestEventProcessing(t *testing.T) {
	store := NewMemoryStore()

	eventKey := "event-123"

	// Event should not be processed initially
	if store.WasProcessed(eventKey) {
		t.Error("expected event to not be processed")
	}

	// Mark event as processed
	err := store.MarkProcessed(eventKey, 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error marking event: %v", err)
	}

	// Event should now be processed
	if !store.WasProcessed(eventKey) {
		t.Error("expected event to be processed")
	}
}

func TestNotificationOperations(t *testing.T) {
	store := NewMemoryStore()

	prURL := "https://github.com/test/repo/pull/123"

	// Last notification should be zero time initially
	lastNotif := store.LastNotification(prURL)
	if !lastNotif.IsZero() {
		t.Error("expected zero time for non-existent notification")
	}

	// Record notification
	notifiedAt := time.Now()
	err := store.RecordNotification(prURL, notifiedAt)
	if err != nil {
		t.Fatalf("unexpected error recording notification: %v", err)
	}

	// Retrieve last notification
	retrieved := store.LastNotification(prURL)
	if !retrieved.Equal(notifiedAt) {
		t.Errorf("expected notifiedAt %v, got %v", notifiedAt, retrieved)
	}
}

func TestCleanup(t *testing.T) {
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
	err := store.Cleanup()
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
