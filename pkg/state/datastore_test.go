package state

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/ds9/pkg/datastore"
)

func TestNewDatastoreStore(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	ctx := context.Background()

	// Create store with mock client
	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}

	if store == nil {
		t.Fatal("expected non-nil store")
	}

	if store.memory == nil {
		t.Error("expected non-nil memory store")
	}

	if store.disabled {
		t.Error("expected store to not be disabled")
	}

	// Test connectivity by doing a Put/Get with proper entity
	testKey := datastore.NameKey(kindEvent, "test-key", nil)
	testEntity := &eventEntity{
		EventKey:  "test",
		Processed: time.Now(),
	}
	_, err := client.Put(ctx, testKey, testEntity)
	if err != nil {
		t.Fatalf("connectivity test failed: %v", err)
	}

	// Clean up
	store.Close()
}

func TestDatastoreStore_ThreadOperations(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	// Test non-existent thread
	_, exists := store.Thread("owner", "repo", 123, "C123")
	if exists {
		t.Error("expected thread to not exist")
	}

	// Save thread
	threadInfo := ThreadInfo{
		ThreadTS:      "1234567890.123456",
		ChannelID:     "C123",
		LastState:     "awaiting_review",
		MessageText:   "Test PR",
		UpdatedAt:     time.Now(),
		LastEventTime: time.Now(),
	}

	err := store.SaveThread("owner", "repo", 123, "C123", threadInfo)
	if err != nil {
		t.Fatalf("unexpected error saving thread: %v", err)
	}

	// Retrieve from memory cache (immediate)
	retrieved, exists := store.Thread("owner", "repo", 123, "C123")
	if !exists {
		t.Fatal("expected thread to exist in memory cache")
	}

	if retrieved.ThreadTS != threadInfo.ThreadTS {
		t.Errorf("expected ThreadTS %s, got %s", threadInfo.ThreadTS, retrieved.ThreadTS)
	}

	// Give async Datastore write time to complete
	time.Sleep(100 * time.Millisecond)

	// Clear memory cache to test Datastore retrieval
	store.memory = NewMemoryStore()

	// Retrieve from Datastore
	retrieved, exists = store.Thread("owner", "repo", 123, "C123")
	if !exists {
		t.Fatal("expected thread to exist in Datastore")
	}

	if retrieved.ThreadTS != threadInfo.ThreadTS {
		t.Errorf("expected ThreadTS %s from Datastore, got %s", threadInfo.ThreadTS, retrieved.ThreadTS)
	}
}

func TestDatastoreStore_DMOperations(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	prURL := "https://github.com/test/repo/pull/123"

	// Test non-existent DM
	_, exists := store.LastDM("U001", prURL)
	if exists {
		t.Error("expected DM to not exist")
	}

	// Record DM
	sentAt := time.Now().Truncate(time.Millisecond)
	err := store.RecordDM("U001", prURL, sentAt)
	if err != nil {
		t.Fatalf("unexpected error recording DM: %v", err)
	}

	// Retrieve from memory cache
	retrieved, exists := store.LastDM("U001", prURL)
	if !exists {
		t.Fatal("expected DM to exist in memory cache")
	}

	if !retrieved.Truncate(time.Millisecond).Equal(sentAt) {
		t.Errorf("expected sentAt %v, got %v", sentAt, retrieved)
	}

	// Give async Datastore write time to complete
	time.Sleep(100 * time.Millisecond)

	// Clear memory cache to test Datastore retrieval
	store.memory = NewMemoryStore()

	// Retrieve from Datastore
	retrieved, exists = store.LastDM("U001", prURL)
	if !exists {
		t.Fatal("expected DM to exist in Datastore")
	}

	if !retrieved.Truncate(time.Millisecond).Equal(sentAt) {
		t.Errorf("expected sentAt %v from Datastore, got %v", sentAt, retrieved)
	}
}

func TestDatastoreStore_DMMessageOperations(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	prURL := "https://github.com/test/repo/pull/123"

	// Test non-existent DM message
	_, exists := store.DMMessage("U001", prURL)
	if exists {
		t.Error("expected DM message to not exist")
	}

	// Save DM message
	dmInfo := DMInfo{
		SentAt:      time.Now().Truncate(time.Millisecond),
		ChannelID:   "D001",
		MessageTS:   "1234567890.123456",
		MessageText: "Test DM message",
	}

	err := store.SaveDMMessage("U001", prURL, dmInfo)
	if err != nil {
		t.Fatalf("unexpected error saving DM message: %v", err)
	}

	// Retrieve from memory cache
	retrieved, exists := store.DMMessage("U001", prURL)
	if !exists {
		t.Fatal("expected DM message to exist in memory cache")
	}

	if retrieved.MessageTS != dmInfo.MessageTS {
		t.Errorf("expected MessageTS %s, got %s", dmInfo.MessageTS, retrieved.MessageTS)
	}

	// Give async Datastore write time to complete
	time.Sleep(100 * time.Millisecond)

	// Clear memory cache to test Datastore retrieval
	store.memory = NewMemoryStore()

	// Retrieve from Datastore
	retrieved, exists = store.DMMessage("U001", prURL)
	if !exists {
		t.Fatal("expected DM message to exist in Datastore")
	}

	if retrieved.MessageTS != dmInfo.MessageTS {
		t.Errorf("expected MessageTS %s from Datastore, got %s", dmInfo.MessageTS, retrieved.MessageTS)
	}
}

func TestDatastoreStore_ListDMUsers(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer func() {
		// Give async operations plenty of time to complete before cleanup
		time.Sleep(500 * time.Millisecond)
		cleanup()
	}()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

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

	// List from memory cache (fast path)
	users := store.ListDMUsers(prURL)
	if len(users) != 3 {
		t.Fatalf("expected 3 users from memory, got %d", len(users))
	}

	// Give async Datastore writes time to complete
	time.Sleep(200 * time.Millisecond)

	// Clear memory cache to test Datastore query
	store.memory = NewMemoryStore()

	// List from Datastore
	users = store.ListDMUsers(prURL)
	if len(users) != 3 {
		t.Fatalf("expected 3 users from Datastore, got %d", len(users))
	}
}

func TestDatastoreStore_DigestOperations(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	userID := "U001"
	date := "2025-01-15"

	// Test non-existent digest
	_, exists := store.LastDigest(userID, date)
	if exists {
		t.Error("expected digest to not exist")
	}

	// Record digest
	sentAt := time.Now().Truncate(time.Millisecond)
	err := store.RecordDigest(userID, date, sentAt)
	if err != nil {
		t.Fatalf("unexpected error recording digest: %v", err)
	}

	// Retrieve from memory cache
	retrieved, exists := store.LastDigest(userID, date)
	if !exists {
		t.Fatal("expected digest to exist in memory cache")
	}

	if !retrieved.Truncate(time.Millisecond).Equal(sentAt) {
		t.Errorf("expected sentAt %v, got %v", sentAt, retrieved)
	}

	// Give Datastore write time to complete (synchronous for digests)
	time.Sleep(100 * time.Millisecond)

	// Clear memory cache to test Datastore retrieval
	store.memory = NewMemoryStore()

	// Retrieve from Datastore
	retrieved, exists = store.LastDigest(userID, date)
	if !exists {
		t.Fatal("expected digest to exist in Datastore")
	}

	if !retrieved.Truncate(time.Millisecond).Equal(sentAt) {
		t.Errorf("expected sentAt %v from Datastore, got %v", sentAt, retrieved)
	}
}

func TestDatastoreStore_EventDeduplication(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	eventKey := "webhook-12345"

	// Test non-existent event
	if store.WasProcessed(eventKey) {
		t.Error("expected event to not be processed")
	}

	// Mark event as processed
	err := store.MarkProcessed(eventKey, 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error marking event: %v", err)
	}

	// Check memory cache immediately
	if !store.WasProcessed(eventKey) {
		t.Error("expected event to be processed in memory cache")
	}

	// Give Datastore transaction time to complete
	time.Sleep(200 * time.Millisecond)

	// Clear memory cache to test Datastore check
	store.memory = NewMemoryStore()

	// Check Datastore
	if !store.WasProcessed(eventKey) {
		t.Error("expected event to be processed in Datastore")
	}

	// Try to mark again - should return ErrAlreadyProcessed
	err = store.MarkProcessed(eventKey, 24*time.Hour)
	if err != ErrAlreadyProcessed {
		t.Errorf("expected ErrAlreadyProcessed, got %v", err)
	}
}

func TestDatastoreStore_NotificationTracking(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	prURL := "https://github.com/test/repo/pull/123"

	// Test non-existent notification
	lastNotif := store.LastNotification(prURL)
	if !lastNotif.IsZero() {
		t.Error("expected zero time for non-existent notification")
	}

	// Record notification
	notifiedAt := time.Now().Truncate(time.Millisecond)
	err := store.RecordNotification(prURL, notifiedAt)
	if err != nil {
		t.Fatalf("unexpected error recording notification: %v", err)
	}

	// Give async Datastore write time to complete
	time.Sleep(100 * time.Millisecond)

	// Retrieve from Datastore
	retrieved := store.LastNotification(prURL)
	if retrieved.IsZero() {
		t.Fatal("expected non-zero time from Datastore")
	}

	if !retrieved.Truncate(time.Millisecond).Equal(notifiedAt) {
		t.Errorf("expected notifiedAt %v, got %v", notifiedAt, retrieved)
	}
}

func TestDatastoreStore_DisabledMode(t *testing.T) {
	// Create store in disabled mode (no Datastore client)
	store := &DatastoreStore{
		ds:       nil,
		memory:   NewMemoryStore(),
		disabled: true,
	}
	defer store.Close()

	// All operations should work with memory only
	threadInfo := ThreadInfo{
		ThreadTS:      "1234567890.123456",
		ChannelID:     "C123",
		LastState:     "awaiting_review",
		MessageText:   "Test PR",
		LastEventTime: time.Now(),
	}

	err := store.SaveThread("owner", "repo", 123, "C123", threadInfo)
	if err != nil {
		t.Fatalf("unexpected error in disabled mode: %v", err)
	}

	retrieved, exists := store.Thread("owner", "repo", 123, "C123")
	if !exists {
		t.Fatal("expected thread to exist in memory")
	}

	if retrieved.ThreadTS != threadInfo.ThreadTS {
		t.Errorf("expected ThreadTS %s, got %s", threadInfo.ThreadTS, retrieved.ThreadTS)
	}
}

func TestDatastoreStore_Cleanup(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	// Add some old data to memory
	oldTime := time.Now().Add(-100 * 24 * time.Hour)
	store.memory.threads[threadKey("owner", "repo", 1, "C123")] = ThreadInfo{UpdatedAt: oldTime}

	// Run cleanup
	err := store.Cleanup()
	if err != nil {
		t.Fatalf("unexpected error during cleanup: %v", err)
	}

	// Verify memory was cleaned
	if len(store.memory.threads) != 0 {
		t.Errorf("expected 0 threads after cleanup, got %d", len(store.memory.threads))
	}
}

func TestDatastoreStore_Close(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}

	// Close should not error
	err := store.Close()
	if err != nil {
		t.Errorf("unexpected error closing store: %v", err)
	}

	// Closing disabled store should also work
	disabledStore := &DatastoreStore{
		ds:       nil,
		memory:   NewMemoryStore(),
		disabled: true,
	}

	err = disabledStore.Close()
	if err != nil {
		t.Errorf("unexpected error closing disabled store: %v", err)
	}
}

func TestDatastoreStore_MemoryFirstFallback(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}

	// Save thread to memory only (don't wait for async Datastore write)
	threadInfo := ThreadInfo{
		ThreadTS:      "1234567890.123456",
		ChannelID:     "C123",
		LastState:     "awaiting_review",
		MessageText:   "Test PR",
		UpdatedAt:     time.Now(),
		LastEventTime: time.Now(),
	}

	store.SaveThread("owner", "repo", 123, "C123", threadInfo)

	// Immediate retrieval should hit memory cache (fast path)
	start := time.Now()
	retrieved, exists := store.Thread("owner", "repo", 123, "C123")
	elapsed := time.Since(start)

	if !exists {
		t.Fatal("expected thread to exist")
	}

	if retrieved.ThreadTS != threadInfo.ThreadTS {
		t.Errorf("expected ThreadTS %s, got %s", threadInfo.ThreadTS, retrieved.ThreadTS)
	}

	// Memory cache should be very fast (< 1ms)
	if elapsed > 10*time.Millisecond {
		t.Logf("warning: memory cache retrieval took %v (expected < 10ms)", elapsed)
	}

	// Give async goroutine time to complete before store.Close()
	time.Sleep(500 * time.Millisecond)
	store.Close()
}

func TestDatastoreStore_PendingDMOperations(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	// Test retrieval when no pending DMs exist
	pending, err := store.GetPendingDMs(time.Now())
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending DMs, got %d", len(pending))
	}

	// Queue a DM that should be sent now
	now := time.Now().Truncate(time.Millisecond)
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

	// Get pending DMs from memory cache (fast path)
	pending, err = store.GetPendingDMs(now)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs: %v", err)
	}

	// Only dm1 should be returned (dm2 is in the future)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending DM from memory, got %d", len(pending))
	}

	if pending[0].ID != "dm-001" {
		t.Errorf("expected DM ID dm-001, got %s", pending[0].ID)
	}

	// Give async Datastore writes time to complete
	time.Sleep(200 * time.Millisecond)

	// Clear memory cache to test Datastore retrieval
	store.memory = NewMemoryStore()

	// Get pending DMs from Datastore
	// Note: The mock Datastore may return all DMs regardless of filter
	// In production, the filter would work correctly
	future := now.Add(15 * time.Minute)
	pending, err = store.GetPendingDMs(future)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs from Datastore: %v", err)
	}

	// Should get both DMs from Datastore
	if len(pending) < 2 {
		t.Fatalf("expected at least 2 pending DMs from Datastore, got %d", len(pending))
	}

	// Verify both DMs are present
	dmIDs := make(map[string]bool)
	for _, dm := range pending {
		dmIDs[dm.ID] = true
	}
	if !dmIDs["dm-001"] {
		t.Error("expected dm-001 to be in Datastore")
	}
	if !dmIDs["dm-002"] {
		t.Error("expected dm-002 to be in Datastore")
	}

	// Remove dm-001
	err = store.RemovePendingDM("dm-001")
	if err != nil {
		t.Fatalf("unexpected error removing DM: %v", err)
	}

	// Give async Datastore delete time to complete
	time.Sleep(200 * time.Millisecond)

	// Clear memory again to force Datastore query
	store.memory = NewMemoryStore()

	// Now only dm-002 should remain in Datastore (query in future to catch it)
	futureLater := now.Add(15 * time.Minute)
	pending, err = store.GetPendingDMs(futureLater)
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

func TestDatastoreStore_PendingDMDisabledMode(t *testing.T) {
	// Create store in disabled mode (no Datastore client)
	store := &DatastoreStore{
		ds:       nil,
		memory:   NewMemoryStore(),
		disabled: true,
	}
	defer store.Close()

	now := time.Now()

	// Queue DM in memory-only mode
	dm := PendingDM{
		ID:        "dm-001",
		UserID:    "U001",
		PRURL:     "https://github.com/test/repo/pull/123",
		SendAfter: now.Add(-5 * time.Minute),
	}

	err := store.QueuePendingDM(dm)
	if err != nil {
		t.Fatalf("unexpected error queueing DM in disabled mode: %v", err)
	}

	// Get pending DMs from memory
	pending, err := store.GetPendingDMs(now)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs in disabled mode: %v", err)
	}

	if len(pending) != 1 {
		t.Fatalf("expected 1 pending DM in disabled mode, got %d", len(pending))
	}

	// Remove DM
	err = store.RemovePendingDM("dm-001")
	if err != nil {
		t.Fatalf("unexpected error removing DM in disabled mode: %v", err)
	}

	// Verify removed
	pending, err = store.GetPendingDMs(now)
	if err != nil {
		t.Fatalf("unexpected error getting pending DMs after removal: %v", err)
	}

	if len(pending) != 0 {
		t.Errorf("expected 0 pending DMs after removal, got %d", len(pending))
	}
}

func TestDatastoreStore_PendingDMCleanup(t *testing.T) {
	client, cleanup := datastore.NewMockClient(t)
	defer cleanup()

	store := &DatastoreStore{
		ds:       client,
		memory:   NewMemoryStore(),
		disabled: false,
	}
	defer store.Close()

	now := time.Now().Truncate(time.Millisecond)
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

	// Give async writes time to complete
	time.Sleep(200 * time.Millisecond)

	// Run cleanup
	err := store.Cleanup()
	if err != nil {
		t.Fatalf("unexpected error during cleanup: %v", err)
	}

	// Verify old DM was removed from memory
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
