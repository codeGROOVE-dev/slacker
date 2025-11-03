package cache

import (
	"sync"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
)

// ThreadInfo is an alias to state.ThreadInfo to avoid duplication.
type ThreadInfo = state.ThreadInfo

// ThreadCache manages PR thread IDs for a workspace.
//
//nolint:govet // Field order optimized for logical grouping over memory alignment
type ThreadCache struct {
	mu           sync.RWMutex
	creationLock sync.Mutex            // Prevents concurrent creation of the same PR thread
	prThreads    map[string]ThreadInfo // "owner/repo#123:channelID" -> thread info
	creating     map[string]bool       // Track PRs currently being created
}

// New creates a new ThreadCache with initialized maps.
func New() *ThreadCache {
	return &ThreadCache{
		prThreads: make(map[string]ThreadInfo),
		creating:  make(map[string]bool),
	}
}

// Get retrieves thread info for a PR.
func (tc *ThreadCache) Get(prKey string) (ThreadInfo, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	info, exists := tc.prThreads[prKey]
	return info, exists
}

// Set stores thread info for a PR.
func (tc *ThreadCache) Set(prKey string, info ThreadInfo) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	info.UpdatedAt = time.Now()
	tc.prThreads[prKey] = info
}

// SetForTest stores thread info with a specific UpdatedAt time (for testing only).
func (tc *ThreadCache) SetForTest(prKey string, info ThreadInfo) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	// Don't override UpdatedAt - use the one provided in info
	tc.prThreads[prKey] = info
}

// Cleanup removes entries older than the specified age.
// This prevents unbounded memory growth for closed/merged PRs.
func (tc *ThreadCache) Cleanup(maxAge time.Duration) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for key, info := range tc.prThreads {
		if info.UpdatedAt.Before(cutoff) {
			delete(tc.prThreads, key)
		}
	}
}

// MarkCreating marks a PR as currently being created to prevent duplicates.
// Returns false if already marked as creating.
func (tc *ThreadCache) MarkCreating(prKey string) bool {
	tc.creationLock.Lock()
	defer tc.creationLock.Unlock()

	if tc.creating[prKey] {
		return false // Already being created
	}
	tc.creating[prKey] = true
	return true
}

// UnmarkCreating removes the creating marker for a PR.
func (tc *ThreadCache) UnmarkCreating(prKey string) {
	tc.creationLock.Lock()
	defer tc.creationLock.Unlock()
	delete(tc.creating, prKey)
}

// IsCreating checks if a PR is currently being created.
func (tc *ThreadCache) IsCreating(prKey string) bool {
	tc.creationLock.Lock()
	defer tc.creationLock.Unlock()
	return tc.creating[prKey]
}
