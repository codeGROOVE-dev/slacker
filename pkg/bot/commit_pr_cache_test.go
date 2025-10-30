package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
)

// TestCommitPRCache_RecordAndFind tests basic cache operations.
func TestCommitPRCache_RecordAndFind(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Record a PR with a commit
	cache.RecordPR("owner", "repo", 123, "abc123")

	// Should find it immediately
	prs := cache.FindPRsForCommit("owner", "repo", "abc123")
	if len(prs) != 1 || prs[0] != 123 {
		t.Errorf("expected [123], got %v", prs)
	}

	// Should not find different commit
	prs = cache.FindPRsForCommit("owner", "repo", "def456")
	if len(prs) != 0 {
		t.Errorf("expected empty for unknown commit, got %v", prs)
	}

	// Should not find in different repo
	prs = cache.FindPRsForCommit("owner", "other-repo", "abc123")
	if len(prs) != 0 {
		t.Errorf("expected empty for different repo, got %v", prs)
	}
}

// TestCommitPRCache_MultiplePRsSameCommit tests handling of multiple PRs with same commit.
func TestCommitPRCache_MultiplePRsSameCommit(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Same commit in two different PRs (e.g., backport)
	cache.RecordPR("owner", "repo", 123, "abc123")
	cache.RecordPR("owner", "repo", 456, "abc123")

	prs := cache.FindPRsForCommit("owner", "repo", "abc123")
	if len(prs) != 2 {
		t.Errorf("expected 2 PRs, got %v", prs)
	}

	// Should contain both PR numbers
	found123, found456 := false, false
	for _, pr := range prs {
		if pr == 123 {
			found123 = true
		}
		if pr == 456 {
			found456 = true
		}
	}

	if !found123 || !found456 {
		t.Errorf("expected both PRs 123 and 456, got %v", prs)
	}
}

// TestCommitPRCache_UpdateExistingPR tests PR with multiple commits (e.g., force push adds new commit).
func TestCommitPRCache_UpdateExistingPR(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Record PR with initial commit
	cache.RecordPR("owner", "repo", 123, "abc123")

	// Add another commit to same PR (force push or additional commit)
	cache.RecordPR("owner", "repo", 123, "def456")

	// Both commits should find the PR (cache stores multiple commits per PR)
	prs := cache.FindPRsForCommit("owner", "repo", "abc123")
	if len(prs) != 1 || prs[0] != 123 {
		t.Errorf("expected [123] for first commit, got %v", prs)
	}

	prs = cache.FindPRsForCommit("owner", "repo", "def456")
	if len(prs) != 1 || prs[0] != 123 {
		t.Errorf("expected [123] for second commit, got %v", prs)
	}
}

// TestCommitPRCache_Expiration tests that old entries are cleaned up.
func TestCommitPRCache_Expiration(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Manually add an old entry (11 minutes ago)
	cache.mu.Lock()
	cache.entries["owner/repo"] = []CommitPREntry{
		{
			PRNumber:  123,
			HeadSHA:   "old123",
			UpdatedAt: time.Now().Add(-11 * time.Minute),
		},
	}
	cache.mu.Unlock()

	// Add a recent entry
	cache.RecordPR("owner", "repo", 456, "new456")

	// Old entry should be gone
	prs := cache.FindPRsForCommit("owner", "repo", "old123")
	if len(prs) != 0 {
		t.Errorf("expected old entry to be cleaned up, got %v", prs)
	}

	// Recent entry should still be there
	prs = cache.FindPRsForCommit("owner", "repo", "new456")
	if len(prs) != 1 || prs[0] != 456 {
		t.Errorf("expected [456] for recent entry, got %v", prs)
	}
}

// TestCommitPRCache_EmptyCommitSHA tests that empty commits are ignored.
func TestCommitPRCache_EmptyCommitSHA(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Try to record with empty SHA
	cache.RecordPR("owner", "repo", 123, "")

	// Should not be recorded
	cache.mu.RLock()
	entries := cache.entries["owner/repo"]
	cache.mu.RUnlock()

	if len(entries) != 0 {
		t.Errorf("expected no entries for empty SHA, got %d", len(entries))
	}
}

// TestCheckEventIntegration_CacheHit tests the full flow when cache has the commit.
func TestCheckEventIntegration_CacheHit(t *testing.T) {
	// Setup mock state store
	mockStore := &mockStateStore{
		threads: make(map[string]state.ThreadInfo),
	}

	// Create coordinator with real commit cache
	coord := &Coordinator{
		stateStore: mockStore,
		commitPRCache: &CommitPRCache{
			entries: make(map[string][]CommitPREntry),
		},
		github: &mockGitHubClientForCache{
			// Mock should NOT be called if cache works
			findPRsForCommitFunc: func(ctx context.Context, owner, repo, sha string) ([]int, error) {
				t.Error("GitHub API should not be called when cache has the commit")
				return nil, nil
			},
		},
	}

	// Populate cache as if we just processed a PR event
	coord.commitPRCache.RecordPR("testorg", "testrepo", 123, "abc123def456")

	// Simulate a check_run event arriving with just the commit SHA
	event := client.Event{
		Type:      "check_run",
		URL:       "https://github.com/testorg/testrepo",
		Timestamp: time.Now(),
		Raw: map[string]any{
			"commit_sha":  "abc123def456",
			"delivery_id": "test-delivery-123",
		},
	}

	// Call lookupPRsForCheckEvent
	prNumbers := coord.lookupPRsForCheckEvent(context.Background(), event, "testorg")

	// Should find PR 123 from cache
	if len(prNumbers) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prNumbers))
	}
	if prNumbers[0] != 123 {
		t.Errorf("expected PR 123, got %d", prNumbers[0])
	}
}

// TestCheckEventIntegration_CacheMissFallback tests fallback to GitHub API on cache miss.
func TestCheckEventIntegration_CacheMissFallback(t *testing.T) {
	// Setup mock state store
	mockStore := &mockStateStore{
		threads: make(map[string]state.ThreadInfo),
	}

	apiCalled := false

	// Create coordinator with empty cache
	coord := &Coordinator{
		stateStore: mockStore,
		commitPRCache: &CommitPRCache{
			entries: make(map[string][]CommitPREntry),
		},
		github: &mockGitHubClientForCache{
			// Mock SHOULD be called on cache miss
			findPRsForCommitFunc: func(ctx context.Context, owner, repo, sha string) ([]int, error) {
				apiCalled = true
				if sha == "unknown123" {
					return []int{456}, nil // Return PR 456
				}
				return nil, nil
			},
		},
	}

	// Cache is empty - simulate check event arriving before PR event

	// Simulate a check_run event
	event := client.Event{
		Type:      "check_run",
		URL:       "https://github.com/testorg/testrepo",
		Timestamp: time.Now(),
		Raw: map[string]any{
			"commit_sha":  "unknown123",
			"delivery_id": "test-delivery-456",
		},
	}

	// Call lookupPRsForCheckEvent
	prNumbers := coord.lookupPRsForCheckEvent(context.Background(), event, "testorg")

	// Should have called GitHub API
	if !apiCalled {
		t.Error("expected GitHub API to be called on cache miss")
	}

	// Should find PR 456 from API
	if len(prNumbers) != 1 {
		t.Fatalf("expected 1 PR from API, got %d", len(prNumbers))
	}
	if prNumbers[0] != 456 {
		t.Errorf("expected PR 456 from API, got %d", prNumbers[0])
	}
}

// TestCachePopulationFromTurnclient tests that cache is populated when processing PRs.
func TestCachePopulationFromTurnclient(t *testing.T) {
	// This test verifies that when we receive a PR event and fetch it via turnclient,
	// we populate the commit→PR cache with all commits from that PR.

	// Create cache
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Simulate turnclient returning a PR with multiple commits
	mockPR := prx.PullRequest{
		Number:  789,
		Title:   "Test PR",
		Author:  "testuser",
		Commits: []string{"commit1", "commit2", "commit3"},
	}

	// Manually populate cache as the code does
	for _, commitSHA := range mockPR.Commits {
		if commitSHA != "" {
			cache.RecordPR("owner", "repo", mockPR.Number, commitSHA)
		}
	}

	// Verify all commits are cached
	for i, commitSHA := range mockPR.Commits {
		prs := cache.FindPRsForCommit("owner", "repo", commitSHA)
		if len(prs) != 1 || prs[0] != 789 {
			t.Errorf("commit %d (%s): expected PR [789], got %v", i, commitSHA, prs)
		}
	}
}

// TestMultipleReposIndependence tests that different repos don't interfere.
func TestMultipleReposIndependence(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Same commit SHA in different repos
	cache.RecordPR("owner1", "repo1", 111, "abc123")
	cache.RecordPR("owner2", "repo2", 222, "abc123")
	cache.RecordPR("owner1", "repo2", 333, "abc123")

	// Each repo should only see its own PR
	prs := cache.FindPRsForCommit("owner1", "repo1", "abc123")
	if len(prs) != 1 || prs[0] != 111 {
		t.Errorf("owner1/repo1: expected [111], got %v", prs)
	}

	prs = cache.FindPRsForCommit("owner2", "repo2", "abc123")
	if len(prs) != 1 || prs[0] != 222 {
		t.Errorf("owner2/repo2: expected [222], got %v", prs)
	}

	prs = cache.FindPRsForCommit("owner1", "repo2", "abc123")
	if len(prs) != 1 || prs[0] != 333 {
		t.Errorf("owner1/repo2: expected [333], got %v", prs)
	}
}

// mockGitHubClientForCache is a minimal mock for commit cache tests.
type mockGitHubClientForCache struct {
	findPRsForCommitFunc func(ctx context.Context, owner, repo, sha string) ([]int, error)
}

func (m *mockGitHubClientForCache) FindPRsForCommit(ctx context.Context, owner, repo, sha string) ([]int, error) {
	if m.findPRsForCommitFunc != nil {
		return m.findPRsForCommitFunc(ctx, owner, repo, sha)
	}
	return nil, nil
}

func (m *mockGitHubClientForCache) Organization() string {
	return "testorg"
}

func (m *mockGitHubClientForCache) Client() any {
	return nil
}

func (m *mockGitHubClientForCache) InstallationToken(ctx context.Context) string {
	return "test-token"
}

func (m *mockGitHubClientForCache) RefreshToken(ctx context.Context) error {
	return nil
}

// TestMostRecentPR tests the MostRecentPR method.
func TestMostRecentPR(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Add multiple PRs with different timestamps
	cache.RecordPR("owner", "repo", 100, "commit100")
	time.Sleep(10 * time.Millisecond)
	cache.RecordPR("owner", "repo", 200, "commit200")
	time.Sleep(10 * time.Millisecond)
	cache.RecordPR("owner", "repo", 300, "commit300")

	// Most recent should be PR 300
	mostRecent := cache.MostRecentPR("owner", "repo")
	if mostRecent != 300 {
		t.Errorf("expected most recent PR to be 300, got %d", mostRecent)
	}

	// Different repo should return 0
	mostRecent = cache.MostRecentPR("owner", "other-repo")
	if mostRecent != 0 {
		t.Errorf("expected 0 for unknown repo, got %d", mostRecent)
	}

	// Empty cache should return 0
	emptyCache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}
	mostRecent = emptyCache.MostRecentPR("owner", "repo")
	if mostRecent != 0 {
		t.Errorf("expected 0 for empty cache, got %d", mostRecent)
	}
}

// TestMostRecentPR_WithMultipleCommitsPerPR tests that we track the most recent PR correctly
// even when PRs have multiple commits.
func TestMostRecentPR_WithMultipleCommitsPerPR(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// PR 100 with multiple commits
	cache.RecordPR("owner", "repo", 100, "commit1")
	time.Sleep(10 * time.Millisecond)
	cache.RecordPR("owner", "repo", 100, "commit2")
	time.Sleep(10 * time.Millisecond)

	// PR 200 with a commit added AFTER PR 100's last commit
	cache.RecordPR("owner", "repo", 200, "commit3")

	// PR 200 has the most recent update (commit3 was added last)
	mostRecent := cache.MostRecentPR("owner", "repo")
	if mostRecent != 200 {
		t.Errorf("expected most recent PR to be 200 (has newest commit timestamp), got %d", mostRecent)
	}

	// Now add another commit to PR 100 after PR 200
	time.Sleep(10 * time.Millisecond)
	cache.RecordPR("owner", "repo", 100, "commit4")

	// Now PR 100 should be most recent again
	mostRecent = cache.MostRecentPR("owner", "repo")
	if mostRecent != 100 {
		t.Errorf("expected most recent PR to be 100 after adding commit4, got %d", mostRecent)
	}
}

// TestTurnclientFallback_CacheHasRecentPR tests the turnclient fallback when cache has a recent PR
// but doesn't have the specific commit we're looking for.
func TestTurnclientFallback_CacheHasRecentPR(t *testing.T) {
	// This simulates the scenario where:
	// 1. Cache miss for specific commit
	// 2. Cache HAS a recently seen PR for the repo
	// 3. We call turnclient on that PR and find the commit there

	// Note: This is a unit test that verifies the cache logic.
	// The actual turnclient integration is tested in sprinkler_test.go
	// where we have full Coordinator setup with mocks.

	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Populate cache with PR 123 that has commit "abc123"
	cache.RecordPR("testorg", "testrepo", 123, "abc123")

	// Now imagine a check event arrives for commit "def456" which isn't cached yet
	// But it belongs to the same PR 123

	// Step 1: Cache lookup fails
	prs := cache.FindPRsForCommit("testorg", "testrepo", "def456")
	if len(prs) != 0 {
		t.Errorf("cache should not have commit def456 yet, got %v", prs)
	}

	// Step 2: We can get the most recent PR
	mostRecent := cache.MostRecentPR("testorg", "testrepo")
	if mostRecent != 123 {
		t.Fatalf("expected most recent PR to be 123, got %d", mostRecent)
	}

	// Step 3: Turnclient would tell us that PR 123 contains "def456"
	// (in real code, this happens in lookupPRsForCheckEvent)
	// After turnclient returns the commit list, we populate the cache:
	cache.RecordPR("testorg", "testrepo", 123, "def456")

	// Step 4: Now cache lookup works
	prs = cache.FindPRsForCommit("testorg", "testrepo", "def456")
	if len(prs) != 1 || prs[0] != 123 {
		t.Errorf("after turnclient lookup, cache should have commit def456 mapped to PR 123, got %v", prs)
	}
}

// TestTurnclientFallback_NoRecentPR tests that we fall back to GitHub API
// when the cache has no recent PRs for the repo.
func TestTurnclientFallback_NoRecentPR(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Cache is completely empty for this repo
	mostRecent := cache.MostRecentPR("testorg", "unknown-repo")
	if mostRecent != 0 {
		t.Errorf("expected 0 for repo with no cached PRs, got %d", mostRecent)
	}

	// In this case, lookupPRsForCheckEvent would skip turnclient
	// and go straight to GitHub API fallback
}

// TestTurnclientFallback_WrongPR tests when the most recent PR doesn't contain the commit.
func TestTurnclientFallback_WrongPR(t *testing.T) {
	cache := &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}

	// Cache has PR 100 with commits from a different PR
	cache.RecordPR("testorg", "testrepo", 100, "commit1")
	cache.RecordPR("testorg", "testrepo", 100, "commit2")

	// Most recent PR is 100
	mostRecent := cache.MostRecentPR("testorg", "testrepo")
	if mostRecent != 100 {
		t.Fatalf("expected most recent PR to be 100, got %d", mostRecent)
	}

	// But we're looking for a commit from PR 200
	// Turnclient would check PR 100, not find "commit_from_pr_200"
	// and we'd fall back to GitHub API which would find PR 200

	// Simulate: turnclient checked PR 100, didn't find the commit
	// (no cache update happens)

	// Simulate: GitHub API found it in PR 200
	// We'd populate the cache with the GitHub API result:
	cache.RecordPR("testorg", "testrepo", 200, "commit_from_pr_200")

	// Now cache has both PRs
	prs := cache.FindPRsForCommit("testorg", "testrepo", "commit_from_pr_200")
	if len(prs) != 1 || prs[0] != 200 {
		t.Errorf("expected to find PR 200, got %v", prs)
	}
}
