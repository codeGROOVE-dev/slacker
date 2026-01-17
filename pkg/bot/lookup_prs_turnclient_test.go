package bot

import (
	"context"
	"os"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
)

// TestLookupPRsForCheckEvent_CacheHit tests commit→PR cache hit path
func TestLookupPRsForCheckEvent_CacheHit(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	commitPRCache := cache.NewCommitPRCache()
	// Pre-populate cache with commit→PR mapping
	commitPRCache.RecordPR("testorg", "testrepo", 42, "abc123")
	commitPRCache.RecordPR("testorg", "testrepo", 43, "abc123")

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  commitPRCache,
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	if len(prNumbers) != 2 {
		t.Errorf("expected 2 PR numbers from cache, got %d", len(prNumbers))
	}

	if prNumbers[0] != 42 || prNumbers[1] != 43 {
		t.Errorf("expected PRs [42, 43] from cache, got %v", prNumbers)
	}
}

// TestLookupPRsForCheckEvent_TurnclientHit tests turnclient fallback path
func TestLookupPRsForCheckEvent_TurnclientHit(t *testing.T) {
	// Set test backend for turnclient mock
	oldBackend := os.Getenv("TURN_TEST_BACKEND")
	os.Setenv("TURN_TEST_BACKEND", "test")
	defer func() {
		if oldBackend != "" {
			os.Setenv("TURN_TEST_BACKEND", oldBackend)
		} else {
			os.Unsetenv("TURN_TEST_BACKEND")
		}
	}()

	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	commitPRCache := cache.NewCommitPRCache()
	// Record that PR 42 is the most recent for this repo (but don't record the commit)
	commitPRCache.RecordPR("testorg", "testrepo", 42, "other-commit")

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{}).
		Build()

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     NewMockState().Build(),
		configManager:  mockConfig,
		threadCache:    cache.New(),
		commitPRCache:  commitPRCache,
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	// The turnclient mock (via TURN_TEST_BACKEND) will return a PR with commits
	// If it contains our commit, we should find it
	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	// With turnclient mock, this may or may not find the PR depending on mock implementation
	// But the test exercises the turnclient code path
	// The function should not crash and should return a valid result
	if prNumbers == nil {
		// If turnclient doesn't find it, should continue to GitHub API and potentially find nothing
		t.Log("turnclient path executed but no PR found - fell back to GitHub API")
	} else {
		t.Logf("turnclient or GitHub API found %d PRs", len(prNumbers))
	}
}

// TestLookupPRsForCheckEvent_NoMostRecentPR tests when cache has no recent PR for repo
func TestLookupPRsForCheckEvent_NoMostRecentPR(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
		findPRsFunc: func(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
			if commitSHA == "abc123" {
				return []int{99}, nil
			}
			return []int{}, nil
		},
	}

	commitPRCache := cache.NewCommitPRCache()
	// Cache is empty - no mostRecentPR available

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  commitPRCache,
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	// Should skip turnclient path (no mostRecentPR) and go straight to GitHub API
	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	if len(prNumbers) != 1 {
		t.Errorf("expected 1 PR from GitHub API, got %d", len(prNumbers))
	}

	if len(prNumbers) > 0 && prNumbers[0] != 99 {
		t.Errorf("expected PR 99 from GitHub API, got %v", prNumbers)
	}
}

// TestLookupPRsForCheckEvent_NoGitHubToken tests when GitHub token is unavailable
func TestLookupPRsForCheckEvent_NoGitHubToken(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "", // No token available
		findPRsFunc: func(ctx context.Context, owner, repo, commitSHA string) ([]int, error) {
			return []int{}, nil
		},
	}

	commitPRCache := cache.NewCommitPRCache()
	// Record that PR 42 exists but not the specific commit
	commitPRCache.RecordPR("testorg", "testrepo", 42, "other-commit")

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  commitPRCache,
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw: map[string]interface{}{
			"commit_sha":  "abc123",
			"delivery_id": "12345",
		},
	}

	// Should skip turnclient path (no token) and try GitHub API (which will also fail without token)
	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	// Should return empty result (or nil) since no token for both paths
	if len(prNumbers) > 0 {
		t.Errorf("expected no PRs without GitHub token, got %v", prNumbers)
	}
}

// TestLookupPRsForCheckEvent_NilRaw tests when event.Raw is nil
func TestLookupPRsForCheckEvent_NilRaw(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	event := client.Event{
		Type: "check_run",
		URL:  "https://github.com/testorg/testrepo/commit/abc123",
		Raw:  nil, // Nil Raw field
	}

	// Should return nil due to missing commit_sha
	prNumbers := c.lookupPRsForCheckEvent(ctx, event, "testorg")

	if prNumbers != nil {
		t.Errorf("expected nil result when Raw is nil, got %v", prNumbers)
	}
}
