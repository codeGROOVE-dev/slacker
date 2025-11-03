package cache

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cache := New()
	if cache == nil {
		t.Fatal("New returned nil")
	}
	if cache.prThreads == nil {
		t.Fatal("prThreads not initialized")
	}
	if cache.creating == nil {
		t.Fatal("creating map not initialized")
	}
}

func TestThreadCache_Get(t *testing.T) {
	t.Run("key_not_found", func(t *testing.T) {
		cache := New()
		_, exists := cache.Get("nonexistent")
		if exists {
			t.Error("expected key to not exist")
		}
	})

	t.Run("key_found", func(t *testing.T) {
		cache := New()
		expectedInfo := ThreadInfo{
			ThreadTS:  "123.456",
			ChannelID: "C123",
			UpdatedAt: time.Now(),
		}

		cache.Set("owner/repo#123:C123", expectedInfo)

		info, exists := cache.Get("owner/repo#123:C123")
		if !exists {
			t.Fatal("expected key to exist")
		}
		if info.ThreadTS != expectedInfo.ThreadTS {
			t.Errorf("expected ThreadTS %s, got %s", expectedInfo.ThreadTS, info.ThreadTS)
		}
		if info.ChannelID != expectedInfo.ChannelID {
			t.Errorf("expected ChannelID %s, got %s", expectedInfo.ChannelID, info.ChannelID)
		}
	})
}

func TestThreadCache_Set(t *testing.T) {
	t.Run("set_new_entry", func(t *testing.T) {
		cache := New()
		info := ThreadInfo{
			ThreadTS:  "123.456",
			ChannelID: "C123",
		}

		before := time.Now()
		cache.Set("owner/repo#123:C123", info)
		after := time.Now()

		retrieved, exists := cache.Get("owner/repo#123:C123")
		if !exists {
			t.Fatal("expected entry to exist after Set")
		}
		if retrieved.ThreadTS != "123.456" {
			t.Errorf("expected ThreadTS 123.456, got %s", retrieved.ThreadTS)
		}
		if retrieved.ChannelID != "C123" {
			t.Errorf("expected ChannelID C123, got %s", retrieved.ChannelID)
		}
		// Check UpdatedAt was set
		if retrieved.UpdatedAt.Before(before) || retrieved.UpdatedAt.After(after) {
			t.Errorf("UpdatedAt should be set to current time, got %v", retrieved.UpdatedAt)
		}
	})

	t.Run("overwrite_existing_entry", func(t *testing.T) {
		cache := New()

		// Set initial value
		cache.Set("key", ThreadInfo{ThreadTS: "old", ChannelID: "C_OLD"})

		// Overwrite
		cache.Set("key", ThreadInfo{ThreadTS: "new", ChannelID: "C_NEW"})

		retrieved, _ := cache.Get("key")
		if retrieved.ThreadTS != "new" {
			t.Errorf("expected new ThreadTS, got %s", retrieved.ThreadTS)
		}
		if retrieved.ChannelID != "C_NEW" {
			t.Errorf("expected new ChannelID, got %s", retrieved.ChannelID)
		}
	})
}

func TestThreadCache_SetForTest(t *testing.T) {
	t.Run("preserves_provided_updatedat", func(t *testing.T) {
		cache := New()
		specificTime := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
		info := ThreadInfo{
			ThreadTS:  "123.456",
			ChannelID: "C123",
			UpdatedAt: specificTime,
		}

		cache.SetForTest("key", info)

		retrieved, exists := cache.Get("key")
		if !exists {
			t.Fatal("expected entry to exist")
		}
		if !retrieved.UpdatedAt.Equal(specificTime) {
			t.Errorf("expected UpdatedAt %v, got %v", specificTime, retrieved.UpdatedAt)
		}
	})
}

func TestThreadCache_Cleanup(t *testing.T) {
	t.Run("remove_old_entries", func(t *testing.T) {
		cache := New()

		now := time.Now()
		oldTime := now.Add(-2 * time.Hour)
		recentTime := now.Add(-30 * time.Minute)

		// Add old and recent entries
		cache.SetForTest("old1", ThreadInfo{ThreadTS: "old1", UpdatedAt: oldTime})
		cache.SetForTest("old2", ThreadInfo{ThreadTS: "old2", UpdatedAt: oldTime})
		cache.SetForTest("recent1", ThreadInfo{ThreadTS: "recent1", UpdatedAt: recentTime})
		cache.SetForTest("recent2", ThreadInfo{ThreadTS: "recent2", UpdatedAt: now})

		// Cleanup entries older than 1 hour
		cache.Cleanup(1 * time.Hour)

		// Old entries should be removed
		_, exists := cache.Get("old1")
		if exists {
			t.Error("old1 should have been removed")
		}
		_, exists = cache.Get("old2")
		if exists {
			t.Error("old2 should have been removed")
		}

		// Recent entries should remain
		_, exists = cache.Get("recent1")
		if !exists {
			t.Error("recent1 should still exist")
		}
		_, exists = cache.Get("recent2")
		if !exists {
			t.Error("recent2 should still exist")
		}
	})

	t.Run("cleanup_empty_cache", func(t *testing.T) {
		cache := New()
		cache.Cleanup(1 * time.Hour) // Should not panic
	})

	t.Run("cleanup_all_old", func(t *testing.T) {
		cache := New()

		oldTime := time.Now().Add(-2 * time.Hour)
		cache.SetForTest("old1", ThreadInfo{UpdatedAt: oldTime})
		cache.SetForTest("old2", ThreadInfo{UpdatedAt: oldTime})

		cache.Cleanup(1 * time.Hour)

		// Both should be removed
		_, exists := cache.Get("old1")
		if exists {
			t.Error("all entries should have been removed")
		}
		_, exists = cache.Get("old2")
		if exists {
			t.Error("all entries should have been removed")
		}
	})

	t.Run("cleanup_with_exact_cutoff", func(t *testing.T) {
		cache := New()

		now := time.Now()
		exactCutoff := now.Add(-1 * time.Hour)

		// Entry exactly at cutoff (should be removed - Before cutoff)
		cache.SetForTest("exact", ThreadInfo{UpdatedAt: exactCutoff})
		// Entry just after cutoff (should remain)
		cache.SetForTest("after", ThreadInfo{UpdatedAt: exactCutoff.Add(1 * time.Millisecond)})

		cache.Cleanup(1 * time.Hour)

		_, exists := cache.Get("exact")
		if exists {
			t.Error("entry at exact cutoff should be removed")
		}
		_, exists = cache.Get("after")
		if !exists {
			t.Error("entry after cutoff should remain")
		}
	})
}

func TestThreadCache_MarkCreating(t *testing.T) {
	t.Run("first_mark_succeeds", func(t *testing.T) {
		cache := New()
		success := cache.MarkCreating("owner/repo#123:C123")
		if !success {
			t.Error("first MarkCreating should succeed")
		}

		if !cache.IsCreating("owner/repo#123:C123") {
			t.Error("PR should be marked as creating")
		}
	})

	t.Run("duplicate_mark_fails", func(t *testing.T) {
		cache := New()

		// First mark succeeds
		if !cache.MarkCreating("key") {
			t.Fatal("first MarkCreating should succeed")
		}

		// Second mark fails
		if cache.MarkCreating("key") {
			t.Error("duplicate MarkCreating should fail")
		}
	})

	t.Run("different_keys_independent", func(t *testing.T) {
		cache := New()

		if !cache.MarkCreating("key1") {
			t.Error("marking key1 should succeed")
		}
		if !cache.MarkCreating("key2") {
			t.Error("marking key2 should succeed")
		}

		if !cache.IsCreating("key1") {
			t.Error("key1 should be marked")
		}
		if !cache.IsCreating("key2") {
			t.Error("key2 should be marked")
		}
	})
}

func TestThreadCache_UnmarkCreating(t *testing.T) {
	t.Run("unmark_existing", func(t *testing.T) {
		cache := New()

		cache.MarkCreating("key")
		if !cache.IsCreating("key") {
			t.Fatal("key should be marked as creating")
		}

		cache.UnmarkCreating("key")
		if cache.IsCreating("key") {
			t.Error("key should no longer be marked as creating")
		}
	})

	t.Run("unmark_nonexistent", func(t *testing.T) {
		cache := New()
		cache.UnmarkCreating("nonexistent") // Should not panic
	})

	t.Run("remark_after_unmark", func(t *testing.T) {
		cache := New()

		// Mark, unmark, then mark again
		cache.MarkCreating("key")
		cache.UnmarkCreating("key")

		if !cache.MarkCreating("key") {
			t.Error("should be able to mark again after unmarking")
		}
	})
}

func TestThreadCache_IsCreating(t *testing.T) {
	t.Run("not_marked", func(t *testing.T) {
		cache := New()
		if cache.IsCreating("nonexistent") {
			t.Error("nonexistent key should not be marked as creating")
		}
	})

	t.Run("marked", func(t *testing.T) {
		cache := New()
		cache.MarkCreating("key")

		if !cache.IsCreating("key") {
			t.Error("marked key should return true")
		}
	})

	t.Run("after_unmark", func(t *testing.T) {
		cache := New()
		cache.MarkCreating("key")
		cache.UnmarkCreating("key")

		if cache.IsCreating("key") {
			t.Error("unmarked key should return false")
		}
	})
}

func TestThreadCache_Concurrency(t *testing.T) {
	cache := New()

	// Concurrent operations on different keys
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			key := "key" + string(rune(n))
			info := ThreadInfo{ThreadTS: "123.456", ChannelID: "C123"}

			cache.Set(key, info)
			cache.Get(key)
			cache.MarkCreating(key)
			cache.IsCreating(key)
			cache.UnmarkCreating(key)

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Concurrent operations on same key
	key := "shared"
	for i := 0; i < 10; i++ {
		go func() {
			info := ThreadInfo{ThreadTS: "123.456", ChannelID: "C123"}
			cache.Set(key, info)
			cache.Get(key)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Concurrent MarkCreating on same key (only first should succeed)
	successCount := 0
	resultChan := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			resultChan <- cache.MarkCreating("concurrent-test")
		}()
	}

	for i := 0; i < 10; i++ {
		if <-resultChan {
			successCount++
		}
	}

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful MarkCreating, got %d", successCount)
	}

	// Cleanup concurrency test
	for i := 0; i < 5; i++ {
		go func() {
			cache.Cleanup(1 * time.Hour)
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}

	// If we get here without a race condition, the test passes
}
