package cache

import (
	"sync"
	"time"
)

// CommitPREntry caches recent commit→PR mappings for fast lookup.
type CommitPREntry struct {
	UpdatedAt time.Time
	HeadSHA   string
	PRNumber  int
}

// CommitPRCache provides in-memory caching of commit SHA → PR mappings.
// This allows quick lookup when check events arrive with just a commit SHA,
// avoiding expensive GitHub API calls for recently-seen PRs.
type CommitPRCache struct {
	entries map[string][]CommitPREntry
	mu      sync.RWMutex
}

// NewCommitPRCache creates a new CommitPRCache with initialized maps.
func NewCommitPRCache() *CommitPRCache {
	return &CommitPRCache{
		entries: make(map[string][]CommitPREntry),
	}
}

// RecordPR records a PR's head commit SHA for commit→PR lookups.
// Entries are kept for 10 minutes to handle check events that arrive shortly after PR events.
func (cpc *CommitPRCache) RecordPR(owner, repo string, prNumber int, headSHA string) {
	if headSHA == "" {
		return // Skip empty commits
	}

	cpc.mu.Lock()
	defer cpc.mu.Unlock()

	repoKey := owner + "/" + repo
	now := time.Now()

	// Initialize map if needed
	if cpc.entries == nil {
		cpc.entries = make(map[string][]CommitPREntry)
	}

	// Add new entry
	entry := CommitPREntry{
		PRNumber:  prNumber,
		HeadSHA:   headSHA,
		UpdatedAt: now,
	}

	// Get existing entries for this repo
	entries := cpc.entries[repoKey]

	// Check if this exact PR+commit combination already exists - update timestamp if so
	found := false
	for i := range entries {
		if entries[i].PRNumber == prNumber && entries[i].HeadSHA == headSHA {
			entries[i].UpdatedAt = now // Refresh timestamp
			found = true
			break
		}
	}

	if !found {
		entries = append(entries, entry)
	}

	// Update the map with the modified entries before filtering
	cpc.entries[repoKey] = entries

	// Keep only entries from last 10 minutes (check events usually arrive within seconds)
	cutoff := now.Add(-10 * time.Minute)
	filtered := make([]CommitPREntry, 0, len(entries))
	for i := range entries {
		if entries[i].UpdatedAt.After(cutoff) {
			filtered = append(filtered, entries[i])
		}
	}

	cpc.entries[repoKey] = filtered
}

// FindPRsForCommit finds PRs in a repo that match the given commit SHA.
// Returns PR numbers if found in recent cache (last 10 minutes), nil otherwise.
func (cpc *CommitPRCache) FindPRsForCommit(owner, repo, commitSHA string) []int {
	if commitSHA == "" {
		return nil
	}

	cpc.mu.RLock()
	defer cpc.mu.RUnlock()

	repoKey := owner + "/" + repo
	entries, exists := cpc.entries[repoKey]
	if !exists {
		return nil
	}

	// Check which PRs have this commit
	var prNumbers []int
	for i := range entries {
		if entries[i].HeadSHA == commitSHA {
			prNumbers = append(prNumbers, entries[i].PRNumber)
		}
	}

	return prNumbers
}

// MostRecentPR returns the most recently updated PR number for a repo from the cache.
// Returns 0 if no recent PRs are cached for this repo.
func (cpc *CommitPRCache) MostRecentPR(owner, repo string) int {
	cpc.mu.RLock()
	defer cpc.mu.RUnlock()

	repoKey := owner + "/" + repo
	entries, exists := cpc.entries[repoKey]
	if !exists || len(entries) == 0 {
		return 0
	}

	// Find the entry with the most recent UpdatedAt timestamp
	mostRecent := entries[0]
	for i := 1; i < len(entries); i++ {
		if entries[i].UpdatedAt.After(mostRecent.UpdatedAt) {
			mostRecent = entries[i]
		}
	}

	return mostRecent.PRNumber
}
