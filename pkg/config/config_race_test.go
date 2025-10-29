package config

import (
	"sync"
	"testing"
	"time"
)

// TestConfigCacheRace tests that concurrent cache access doesn't race.
// Run with: go test -race -run TestConfigCacheRace
func TestConfigCacheRace(t *testing.T) {
	cache := &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     1 * time.Hour,
	}

	// Populate cache with test data
	testConfig := &RepoConfig{}
	cache.set("test-org", testConfig)

	// Spawn 100 concurrent goroutines that all try to read from cache
	// This would trigger a race condition if counters weren't atomic
	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()
			// Each goroutine performs 100 cache accesses
			for j := range 100 {
				// Mix of hits and misses
				if j%2 == 0 {
					cache.get("test-org") // Cache hit
				} else {
					cache.get("nonexistent-org") // Cache miss
				}
			}
		}()
	}

	wg.Wait()

	// Verify statistics are consistent
	hits, misses := cache.stats()
	total := hits + misses
	expected := numGoroutines * 100 // 100 goroutines * 100 accesses each

	if total != int64(expected) {
		t.Errorf("cache statistics inconsistent: got %d total accesses (hits=%d, misses=%d), expected %d",
			total, hits, misses, expected)
	}

	t.Logf("Cache statistics after concurrent access: hits=%d, misses=%d, total=%d", hits, misses, total)
}

// TestConfigCacheStatsNoLock verifies that stats() doesn't need locks.
func TestConfigCacheStatsNoLock(t *testing.T) {
	cache := &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     1 * time.Hour,
	}

	testConfig := &RepoConfig{}
	cache.set("test-org", testConfig)

	// Spawn goroutines that continuously read stats while others access cache
	var wg sync.WaitGroup
	done := make(chan struct{})

	// Reader goroutines - continuously call stats()
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					cache.stats() // Should not race with get() calls
				}
			}
		}()
	}

	// Writer goroutines - continuously call get()
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				cache.get("test-org")
			}
		}()
	}

	// Let readers complete, then signal stats readers to stop
	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()

	hits, misses := cache.stats()
	t.Logf("Final cache statistics: hits=%d, misses=%d", hits, misses)
}
