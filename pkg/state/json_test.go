package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewJSONStore(t *testing.T) {
	// Use temp dir for test
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Override cache dir for testing
	oldCacheDir := os.Getenv("XDG_CACHE_HOME")
	os.Setenv("XDG_CACHE_HOME", tempDir)
	defer func() {
		if oldCacheDir != "" {
			os.Setenv("XDG_CACHE_HOME", oldCacheDir)
		} else {
			os.Unsetenv("XDG_CACHE_HOME")
		}
	}()

	store, err := NewJSONStore()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// Clean up
	store.Close()
}

func TestJSONStore_ThreadOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	// Test non-existent thread
	_, exists := store.Thread("owner", "repo", 123, "C123")
	if exists {
		t.Error("expected thread to not exist")
	}

	// Save thread
	threadInfo := ThreadInfo{
		ThreadTS:    "1234567890.123456",
		ChannelID:   "C123",
		LastState:   "awaiting_review",
		MessageText: "Test PR",
	}

	err = store.SaveThread("owner", "repo", 123, "C123", threadInfo)
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
}

func TestJSONStore_DMOperations(t *testing.T) {
	store := &JSONStore{
		baseDir:       os.TempDir(),
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	// Test non-existent DM
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

func TestJSONStore_Persistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create first store instance
	store1 := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	// Save some data
	threadInfo := ThreadInfo{
		ThreadTS:    "1234567890.123456",
		ChannelID:   "C123",
		LastState:   "awaiting_review",
		MessageText: "Test PR",
	}
	store1.SaveThread("owner", "repo", 123, "C123", threadInfo)

	// Save to disk
	err = store1.save()
	if err != nil {
		t.Fatalf("unexpected error saving: %v", err)
	}

	// Create second store instance (simulates restart)
	store2 := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	// Load from disk
	err = store2.load()
	if err != nil {
		t.Fatalf("unexpected error loading: %v", err)
	}

	// Verify data persisted
	retrieved, exists := store2.Thread("owner", "repo", 123, "C123")
	if !exists {
		t.Fatal("expected thread to exist after reload")
	}

	if retrieved.ThreadTS != threadInfo.ThreadTS {
		t.Errorf("expected ThreadTS %s after reload, got %s", threadInfo.ThreadTS, retrieved.ThreadTS)
	}
}

func TestJSONStore_ListDMUsers(t *testing.T) {
	store := &JSONStore{
		baseDir:       os.TempDir(),
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	prURL := "https://github.com/test/repo/pull/123"

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
	users := store.ListDMUsers(prURL)
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}
}

func TestJSONStore_Cleanup(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	// Add old data
	oldTime := time.Now().Add(-100 * 24 * time.Hour)
	recentTime := time.Now()

	// Old and recent threads
	store.threads[threadKey("owner", "repo", 1, "C123")] = ThreadInfo{UpdatedAt: oldTime}
	store.threads[threadKey("owner", "repo", 2, "C456")] = ThreadInfo{UpdatedAt: recentTime}

	// Run cleanup
	err = store.Cleanup()
	if err != nil {
		t.Fatalf("unexpected error during cleanup: %v", err)
	}

	// Verify old data was cleaned up but recent data remains
	if len(store.threads) != 1 {
		t.Errorf("expected 1 thread after cleanup, got %d", len(store.threads))
	}
}

func TestJSONStore_SaveLoad_RoundTrip(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	// Add various types of data
	store.threads["thread1"] = ThreadInfo{ThreadTS: "123", ChannelID: "C1"}
	store.dms["dm1"] = time.Now()
	store.digests["digest1"] = time.Now()
	store.events["event1"] = time.Now()
	store.notifications["notif1"] = time.Now()
	store.modified = true // Mark as modified so save() actually writes

	// Save
	err = store.save()
	if err != nil {
		t.Fatalf("unexpected error saving: %v", err)
	}

	// Verify files were created
	stateFile := filepath.Join(tempDir, "state.json")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Error("expected state.json file to be created")
	}

	// Load into new store
	store2 := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}

	err = store2.load()
	if err != nil {
		t.Fatalf("unexpected error loading: %v", err)
	}

	// Verify data matches
	if len(store2.threads) != 1 {
		t.Errorf("expected 1 thread, got %d", len(store2.threads))
	}
	if len(store2.dms) != 1 {
		t.Errorf("expected 1 dm, got %d", len(store2.dms))
	}
	if len(store2.digests) != 1 {
		t.Errorf("expected 1 digest, got %d", len(store2.digests))
	}
}

func TestJSONStore_PendingDMOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
		pendingDMs:    make(map[string]PendingDM),
	}

	// Test retrieval when no pending DMs exist
	pending, err := store.GetPendingDMs(time.Now())
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

	err = store.QueuePendingDM(dm1)
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

	err = store.QueuePendingDM(dm2)
	if err != nil {
		t.Fatalf("unexpected error queueing second DM: %v", err)
	}

	// Get pending DMs that are ready to send
	pending, err = store.GetPendingDMs(now)
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
	pending, err = store.GetPendingDMs(future)
	if err != nil {
		t.Fatalf("unexpected error getting future pending DMs: %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("expected 2 pending DMs in future, got %d", len(pending))
	}

	// Remove one DM
	err = store.RemovePendingDM("dm-001")
	if err != nil {
		t.Fatalf("unexpected error removing DM: %v", err)
	}

	// Now only dm2 should remain
	pending, err = store.GetPendingDMs(future)
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
	err = store.RemovePendingDM("dm-999")
	if err != nil {
		t.Errorf("unexpected error removing non-existent DM: %v", err)
	}
}

func TestJSONStore_PendingDMPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create first store instance
	store1 := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
		pendingDMs:    make(map[string]PendingDM),
	}

	// Queue some pending DMs
	now := time.Now()
	dm1 := PendingDM{
		ID:        "dm-001",
		UserID:    "U001",
		PRURL:     "https://github.com/test/repo/pull/123",
		PRTitle:   "Test PR",
		SendAfter: now.Add(5 * time.Minute),
	}
	dm2 := PendingDM{
		ID:        "dm-002",
		UserID:    "U002",
		PRURL:     "https://github.com/test/repo/pull/456",
		PRTitle:   "Another PR",
		SendAfter: now.Add(10 * time.Minute),
	}

	store1.QueuePendingDM(dm1)
	store1.QueuePendingDM(dm2)

	// Save to disk (happens automatically in QueuePendingDM via modified flag)
	err = store1.save()
	if err != nil {
		t.Fatalf("unexpected error saving: %v", err)
	}

	// Create second store instance (simulates restart)
	store2 := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
		pendingDMs:    make(map[string]PendingDM),
	}

	// Load from disk
	err = store2.load()
	if err != nil {
		t.Fatalf("unexpected error loading: %v", err)
	}

	// Verify pending DMs persisted
	future := now.Add(15 * time.Minute)
	pending, err := store2.GetPendingDMs(future)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs: %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("expected 2 pending DMs after reload, got %d", len(pending))
	}

	// Verify the data matches
	dmMap := make(map[string]PendingDM)
	for _, dm := range pending {
		dmMap[dm.ID] = dm
	}

	if dmMap["dm-001"].UserID != "U001" {
		t.Errorf("expected UserID U001 for dm-001, got %s", dmMap["dm-001"].UserID)
	}
	if dmMap["dm-002"].UserID != "U002" {
		t.Errorf("expected UserID U002 for dm-002, got %s", dmMap["dm-002"].UserID)
	}
}

func TestJSONStore_PendingDMCleanup(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "slacker-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
		pendingDMs:    make(map[string]PendingDM),
	}

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
	store.QueuePendingDM(oldDM)

	// Add a recent pending DM
	recentDM := PendingDM{
		ID:        "recent-dm",
		UserID:    "U002",
		PRURL:     "https://github.com/test/repo/pull/2",
		QueuedAt:  now,
		SendAfter: now.Add(10 * time.Minute),
	}
	store.QueuePendingDM(recentDM)

	// Run cleanup
	err = store.Cleanup()
	if err != nil {
		t.Fatalf("unexpected error during cleanup: %v", err)
	}

	// Verify old DM was removed
	pending, err := store.GetPendingDMs(now.Add(24 * time.Hour))
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
