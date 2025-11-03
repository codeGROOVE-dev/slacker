package cache

import (
	"testing"
	"time"
)

func TestNewCommitPRCache(t *testing.T) {
	cache := NewCommitPRCache()
	if cache == nil {
		t.Fatal("NewCommitPRCache returned nil")
	}
	if cache.entries == nil {
		t.Fatal("cache.entries not initialized")
	}
}

func TestCommitPRCache_RecordPR(t *testing.T) {
	t.Run("record_new_pr", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "abc123")
		if len(prNumbers) != 1 || prNumbers[0] != 123 {
			t.Errorf("expected [123], got %v", prNumbers)
		}
	})

	t.Run("skip_empty_sha", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "")
		if prNumbers != nil {
			t.Errorf("expected nil for empty SHA, got %v", prNumbers)
		}
	})

	t.Run("update_existing_pr_commit", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Record PR first time
		cache.RecordPR("owner", "repo", 123, "abc123")

		// Record same PR+commit again (should update timestamp, not duplicate)
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "abc123")
		if len(prNumbers) != 1 || prNumbers[0] != 123 {
			t.Errorf("expected single PR [123], got %v", prNumbers)
		}
	})

	t.Run("multiple_prs_same_commit", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")
		cache.RecordPR("owner", "repo", 456, "abc123")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "abc123")
		if len(prNumbers) != 2 {
			t.Errorf("expected 2 PRs, got %v", prNumbers)
		}
	})

	t.Run("different_repos_isolated", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 123, "abc123")
		cache.RecordPR("owner2", "repo2", 456, "abc123")

		prNumbers1 := cache.FindPRsForCommit("owner1", "repo1", "abc123")
		prNumbers2 := cache.FindPRsForCommit("owner2", "repo2", "abc123")

		if len(prNumbers1) != 1 || prNumbers1[0] != 123 {
			t.Errorf("repo1: expected [123], got %v", prNumbers1)
		}
		if len(prNumbers2) != 1 || prNumbers2[0] != 456 {
			t.Errorf("repo2: expected [456], got %v", prNumbers2)
		}
	})

	t.Run("expiration_after_10_minutes", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Manually add an old entry
		cache.mu.Lock()
		repoKey := "owner/repo"
		cache.entries[repoKey] = []CommitPREntry{
			{
				PRNumber:  123,
				HeadSHA:   "old123",
				UpdatedAt: time.Now().Add(-11 * time.Minute), // 11 minutes ago
			},
		}
		cache.mu.Unlock()

		// Record a new PR (this will trigger cleanup)
		cache.RecordPR("owner", "repo", 456, "new456")

		// Old entry should be gone
		oldPRs := cache.FindPRsForCommit("owner", "repo", "old123")
		if oldPRs != nil {
			t.Errorf("expected old entry to be expired, got %v", oldPRs)
		}

		// New entry should exist
		newPRs := cache.FindPRsForCommit("owner", "repo", "new456")
		if len(newPRs) != 1 || newPRs[0] != 456 {
			t.Errorf("expected [456], got %v", newPRs)
		}
	})

	t.Run("initialize_nil_map", func(t *testing.T) {
		cache := &CommitPRCache{} // Uninitialized cache
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "abc123")
		if len(prNumbers) != 1 || prNumbers[0] != 123 {
			t.Errorf("expected [123] after lazy init, got %v", prNumbers)
		}
	})
}

func TestCommitPRCache_FindPRsForCommit(t *testing.T) {
	t.Run("empty_sha", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "")
		if prNumbers != nil {
			t.Errorf("expected nil for empty SHA, got %v", prNumbers)
		}
	})

	t.Run("repo_not_found", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumbers := cache.FindPRsForCommit("other", "repo", "abc123")
		if prNumbers != nil {
			t.Errorf("expected nil for unknown repo, got %v", prNumbers)
		}
	})

	t.Run("commit_not_found", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "xyz999")
		if prNumbers != nil {
			t.Errorf("expected nil for unknown commit, got %v", prNumbers)
		}
	})

	t.Run("single_match", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumbers := cache.FindPRsForCommit("owner", "repo", "abc123")
		if len(prNumbers) != 1 || prNumbers[0] != 123 {
			t.Errorf("expected [123], got %v", prNumbers)
		}
	})

	t.Run("multiple_matches", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")
		cache.RecordPR("owner", "repo", 456, "abc123")
		cache.RecordPR("owner", "repo", 789, "xyz999") // Different commit

		prNumbers := cache.FindPRsForCommit("owner", "repo", "abc123")
		if len(prNumbers) != 2 {
			t.Errorf("expected 2 PRs, got %v", prNumbers)
		}
		// Check both PR numbers are present
		found123 := false
		found456 := false
		for _, pr := range prNumbers {
			if pr == 123 {
				found123 = true
			}
			if pr == 456 {
				found456 = true
			}
		}
		if !found123 || !found456 {
			t.Errorf("expected both 123 and 456, got %v", prNumbers)
		}
	})
}

func TestCommitPRCache_MostRecentPR(t *testing.T) {
	t.Run("repo_not_found", func(t *testing.T) {
		cache := NewCommitPRCache()
		prNumber := cache.MostRecentPR("owner", "repo")
		if prNumber != 0 {
			t.Errorf("expected 0 for unknown repo, got %d", prNumber)
		}
	})

	t.Run("empty_repo", func(t *testing.T) {
		cache := NewCommitPRCache()
		// Initialize empty repo
		cache.mu.Lock()
		cache.entries["owner/repo"] = []CommitPREntry{}
		cache.mu.Unlock()

		prNumber := cache.MostRecentPR("owner", "repo")
		if prNumber != 0 {
			t.Errorf("expected 0 for empty repo, got %d", prNumber)
		}
	})

	t.Run("single_entry", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner", "repo", 123, "abc123")

		prNumber := cache.MostRecentPR("owner", "repo")
		if prNumber != 123 {
			t.Errorf("expected 123, got %d", prNumber)
		}
	})

	t.Run("multiple_entries_different_times", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Add entries with different timestamps
		cache.mu.Lock()
		repoKey := "owner/repo"
		now := time.Now()
		cache.entries[repoKey] = []CommitPREntry{
			{PRNumber: 100, HeadSHA: "old1", UpdatedAt: now.Add(-5 * time.Minute)},
			{PRNumber: 200, HeadSHA: "old2", UpdatedAt: now.Add(-3 * time.Minute)},
			{PRNumber: 300, HeadSHA: "recent", UpdatedAt: now}, // Most recent
		}
		cache.mu.Unlock()

		prNumber := cache.MostRecentPR("owner", "repo")
		if prNumber != 300 {
			t.Errorf("expected 300 (most recent), got %d", prNumber)
		}
	})

	t.Run("most_recent_not_last_in_slice", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Add entries where most recent is in the middle
		cache.mu.Lock()
		repoKey := "owner/repo"
		now := time.Now()
		cache.entries[repoKey] = []CommitPREntry{
			{PRNumber: 100, HeadSHA: "old1", UpdatedAt: now.Add(-5 * time.Minute)},
			{PRNumber: 300, HeadSHA: "recent", UpdatedAt: now}, // Most recent (middle)
			{PRNumber: 200, HeadSHA: "old2", UpdatedAt: now.Add(-3 * time.Minute)},
		}
		cache.mu.Unlock()

		prNumber := cache.MostRecentPR("owner", "repo")
		if prNumber != 300 {
			t.Errorf("expected 300 (most recent in middle), got %d", prNumber)
		}
	})
}

func TestCommitPRCache_Concurrency(t *testing.T) {
	cache := NewCommitPRCache()

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(prNum int) {
			cache.RecordPR("owner", "repo", prNum, "commit"+string(rune(prNum)))
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
		<-done
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func(prNum int) {
			_ = cache.FindPRsForCommit("owner", "repo", "commit"+string(rune(prNum)))
			_ = cache.MostRecentPR("owner", "repo")
			done <- true
		}(i)
	}

	// Wait for all reads
	for i := 0; i < 10; i++ {
		<-done
	}

	// If we get here without a race condition, the test passes
}
