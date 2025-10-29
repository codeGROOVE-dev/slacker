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
