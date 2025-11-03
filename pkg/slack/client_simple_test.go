package slack

import (
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
)

// TestSetTeamID tests the SetTeamID setter.
func TestSetTeamID(t *testing.T) {
	t.Parallel()

	client := &Client{}

	testID := "T12345"
	client.SetTeamID(testID)

	if client.teamID != testID {
		t.Errorf("expected teamID %q, got %q", testID, client.teamID)
	}
}

// TestSetStateStore tests the SetStateStore setter.
func TestSetStateStore(t *testing.T) {
	t.Parallel()

	client := &Client{}
	mockStore := &mockStateStore{}

	client.SetStateStore(mockStore)

	if client.stateStore == nil {
		t.Error("expected stateStore to be set")
	}
}

// TestSetManager tests the SetManager setter.
func TestSetManager(t *testing.T) {
	t.Parallel()

	client := &Client{}
	manager := &Manager{}

	client.SetManager(manager)

	if client.manager != manager {
		t.Error("expected manager to be set")
	}
}

// TestInvalidateWorkspaceCache tests cache invalidation.
func TestInvalidateWorkspaceCache(t *testing.T) {
	t.Parallel()

	// Test with nil manager (should not panic)
	client := &Client{teamID: "T123"}
	client.invalidateWorkspaceCache() // Should not panic

	// Test with manager but no teamID (should not call InvalidateCache)
	client2 := &Client{manager: &Manager{}}
	client2.invalidateWorkspaceCache() // Should not panic

	// Test with both manager and teamID - should invalidate cache
	manager := NewManager("test-secret")
	// Pre-populate manager cache
	testClient := New("test-token", "test-secret")
	testClient.SetTeamID("T456")
	manager.clients["T456"] = testClient
	manager.metadata["T456"] = &WorkspaceMetadata{TeamID: "T456"}

	// Create client with manager and teamID
	client3 := &Client{
		manager: manager,
		teamID:  "T456",
	}

	// Verify cache is populated
	if len(manager.clients) != 1 {
		t.Fatalf("expected 1 client in manager cache, got %d", len(manager.clients))
	}

	// Invalidate workspace cache
	client3.invalidateWorkspaceCache()

	// Verify cache was cleared
	if len(manager.clients) != 0 {
		t.Errorf("expected manager cache to be empty after invalidation, got %d entries", len(manager.clients))
	}
}

// TestInvalidateChannel tests channel cache invalidation.
func TestInvalidateChannel(t *testing.T) {
	t.Parallel()

	client := &Client{
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	// Add a cache entry
	channelID := "C123"
	membershipKey := "bot_in_channel_C123"
	client.cache.set(membershipKey, true, time.Minute)

	// Verify it's in cache
	if _, ok := client.cache.get(membershipKey); !ok {
		t.Fatal("expected cache entry to exist before invalidation")
	}

	// Invalidate channel cache
	client.InvalidateChannel(channelID)

	// Verify it's removed from cache
	if _, ok := client.cache.get(membershipKey); ok {
		t.Error("expected cache entry to be removed after invalidation")
	}
}

// TestCacheSetAndGet tests basic cache operations.
func TestCacheSetAndGet(t *testing.T) {
	t.Parallel()

	cache := &apiCache{
		entries: make(map[string]cacheEntry),
	}

	key := "test_key"
	value := "test_value"
	ttl := time.Minute

	// Set value
	cache.set(key, value, ttl)

	// Get value
	result, ok := cache.get(key)
	if !ok {
		t.Fatal("expected cache entry to exist")
	}

	if result != value {
		t.Errorf("expected value %v, got %v", value, result)
	}
}

// TestCacheInvalidate tests cache invalidation.
func TestCacheInvalidate(t *testing.T) {
	t.Parallel()

	cache := &apiCache{
		entries: make(map[string]cacheEntry),
	}

	key := "test_key"
	value := "test_value"

	// Set value
	cache.set(key, value, time.Minute)

	// Verify it exists
	if _, ok := cache.get(key); !ok {
		t.Fatal("expected cache entry to exist before invalidation")
	}

	// Invalidate
	cache.invalidate(key)

	// Verify it's removed
	if _, ok := cache.get(key); ok {
		t.Error("expected cache entry to be removed after invalidation")
	}
}

// TestCacheGetExpired tests cache expiration.
func TestCacheGetExpired(t *testing.T) {
	t.Parallel()

	cache := &apiCache{
		entries: make(map[string]cacheEntry),
	}

	key := "test_key"
	value := "test_value"

	// Set value with 1 millisecond TTL
	cache.set(key, value, time.Millisecond)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Try to get expired value
	result, ok := cache.get(key)
	if ok {
		t.Error("expected cache entry to be expired")
	}
	if result != nil {
		t.Errorf("expected nil result for expired entry, got %v", result)
	}

	// Verify entry was removed from cache
	cache.mu.Lock()
	_, exists := cache.entries[key]
	cache.mu.Unlock()
	if exists {
		t.Error("expected expired entry to be removed from cache")
	}
}

// mockStateStore implements StateStore for testing.
type mockStateStore struct{}

func (m *mockStateStore) DMMessage(userID, prURL string) (state.DMInfo, bool) {
	return state.DMInfo{}, false
}

func (m *mockStateStore) SaveDMMessage(userID, prURL string, info state.DMInfo) error {
	return nil
}
