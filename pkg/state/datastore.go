package state

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/ds9/pkg/datastore"
)

// DatastoreStore implements Store using Google Cloud Datastore with in-memory cache.
// Uses hybrid approach: in-memory cache for speed, Datastore for persistence.
type DatastoreStore struct {
	ds       *datastore.Client
	memory   *MemoryStore
	disabled bool // If Datastore failed to initialize, memory-only mode
}

// Entity types for Datastore.
const (
	kindThread    = "SlackerThread"
	kindDM        = "SlackerDM"
	kindDMMessage = "SlackerDMMessage"
	kindDigest    = "SlackerDigest"
	kindEvent     = "SlackerEvent"
	kindNotify    = "SlackerNotification"
	kindPendingDM = "SlackerPendingDM"
)

// ErrAlreadyProcessed indicates an event was already processed by another instance.
// This is used for cross-instance deduplication during rolling deployments.
var ErrAlreadyProcessed = errors.New("event already processed by another instance")

// Thread entity for Datastore.
type threadEntity struct {
	UpdatedAt     time.Time `datastore:"updated_at"`
	LastEventTime time.Time `datastore:"last_event_time"`
	ThreadTS      string    `datastore:"thread_ts"`
	ChannelID     string    `datastore:"channel_id"`
	MessageText   string    `datastore:"message_text,noindex"`
}

// DM tracking entity.
type dmEntity struct {
	SentAt time.Time `datastore:"sent_at"`
	UserID string    `datastore:"user_id"`
	PRURL  string    `datastore:"pr_url"`
}

// DM message entity for updating messages.
type dmMessageEntity struct {
	UpdatedAt   time.Time `datastore:"updated_at"`
	SentAt      time.Time `datastore:"sent_at"`
	ChannelID   string    `datastore:"channel_id"`
	MessageTS   string    `datastore:"message_ts"`
	MessageText string    `datastore:"message_text,noindex"`
	LastState   string    `datastore:"last_state"`
}

// Digest tracking entity.
type digestEntity struct {
	SentAt time.Time `datastore:"sent_at"`
	UserID string    `datastore:"user_id"`
	Date   string    `datastore:"date"` // YYYY-MM-DD format
}

// Event deduplication entity.
type eventEntity struct {
	Processed time.Time `datastore:"processed"`
	EventKey  string    `datastore:"event_key"`
}

// Notification tracking entity.
type notifyEntity struct {
	NotifiedAt time.Time `datastore:"notified_at"`
	PRURL      string    `datastore:"pr_url"`
}

// Pending DM entity.
type pendingDMEntity struct {
	QueuedAt      time.Time `datastore:"queued_at"`
	SendAfter     time.Time `datastore:"send_after"`
	PRTitle       string    `datastore:"pr_title,noindex"`
	PRRepo        string    `datastore:"pr_repo"`
	PRURL         string    `datastore:"pr_url"`
	WorkspaceID   string    `datastore:"workspace_id"`
	PRAuthor      string    `datastore:"pr_author"`
	PRState       string    `datastore:"pr_state"`
	WorkflowState string    `datastore:"workflow_state"`
	NextActions   string    `datastore:"next_actions,noindex"`
	ChannelID     string    `datastore:"channel_id"`
	ChannelName   string    `datastore:"channel_name"`
	PROwner       string    `datastore:"pr_owner"`
	UserID        string    `datastore:"user_id"`
	PRNumber      int       `datastore:"pr_number"`
}

// NewDatastoreStore creates a new Datastore-backed store with in-memory cache.
// The databaseID parameter specifies which Datastore database to use (e.g., "slacker", "(default)").
func NewDatastoreStore(ctx context.Context, projectID, databaseID string) (*DatastoreStore, error) {
	// Always create in-memory cache
	memStore := NewMemoryStore()

	// Create Datastore client with specified database
	ds, err := datastore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		slog.Warn("failed to create Datastore client, using memory-only mode",
			"error", err,
			"project_id", projectID,
			"database_id", databaseID,
			"fallback", "in-memory only (no persistence)")
		return &DatastoreStore{
			memory:   memStore,
			disabled: true,
		}, nil
	}

	// Test Datastore connectivity
	testKey := datastore.NameKey(kindEvent, "test", nil)
	testEntity := &eventEntity{
		EventKey:  "test",
		Processed: time.Now(),
	}
	_, err = ds.Put(ctx, testKey, testEntity)
	if err != nil {
		slog.Warn("Datastore connectivity test failed, using memory-only mode",
			"error", err,
			"fallback", "in-memory only (no persistence)")
		if closeErr := ds.Close(); closeErr != nil {
			slog.Debug("failed to close Datastore during test", "error", closeErr)
		}
		return &DatastoreStore{
			memory:   memStore,
			disabled: true,
		}, nil
	}

	// Clean up test entity
	if err := ds.Delete(ctx, testKey); err != nil {
		slog.Debug("failed to delete test entity", "error", err)
	}

	slog.Info("initialized Datastore with in-memory cache",
		"project_id", projectID,
		"database_id", databaseID,
		"mode", "hybrid")

	return &DatastoreStore{
		ds:       ds,
		memory:   memStore,
		disabled: false,
	}, nil
}

// Thread retrieves thread info with memory-first, then Datastore fallback.
func (s *DatastoreStore) Thread(ctx context.Context, owner, repo string, number int, channelID string) (ThreadInfo, bool) {
	key := threadKey(owner, repo, number, channelID)

	// Fast path: Check memory cache first
	info, exists := s.memory.Thread(ctx, owner, repo, number, channelID)
	if exists {
		return info, true
	}

	// Datastore disabled or not available
	if s.disabled || s.ds == nil {
		return ThreadInfo{}, false
	}

	// Try Datastore with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	dsKey := datastore.NameKey(kindThread, key, nil)
	var entity threadEntity

	err := s.ds.Get(timeoutCtx, dsKey, &entity)
	if err != nil {
		if !errors.Is(err, datastore.ErrNoSuchEntity) {
			slog.Debug("Datastore get failed, using cache",
				"key", key,
				"error", err)
		}
		return ThreadInfo{}, false
	}

	// Found in Datastore - update memory cache and return
	result := ThreadInfo{
		ThreadTS:      entity.ThreadTS,
		ChannelID:     entity.ChannelID,
		MessageText:   entity.MessageText,
		UpdatedAt:     entity.UpdatedAt,
		LastEventTime: entity.LastEventTime,
	}

	// Update memory cache (sync - fast)
	if err := s.memory.SaveThread(ctx, owner, repo, number, channelID, result); err != nil {
		slog.Debug("failed to update memory cache for thread", "error", err)
	}

	return result, true
}

// SaveThread saves thread info to memory and Datastore.
func (s *DatastoreStore) SaveThread(ctx context.Context, owner, repo string, number int, channelID string, info ThreadInfo) error {
	key := threadKey(owner, repo, number, channelID)

	// Always save to memory (fast, local)
	if err := s.memory.SaveThread(ctx, owner, repo, number, channelID, info); err != nil {
		slog.Warn("failed to save thread to memory", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Capture client for safe concurrent access
	ds := s.ds

	// Save to Datastore with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	dsKey := datastore.NameKey(kindThread, key, nil)
	entity := &threadEntity{
		ThreadTS:      info.ThreadTS,
		ChannelID:     info.ChannelID,
		MessageText:   info.MessageText,
		UpdatedAt:     time.Now(),
		LastEventTime: info.LastEventTime,
	}

	if _, err := ds.Put(timeoutCtx, dsKey, entity); err != nil {
		slog.Error("failed to save thread to Datastore",
			"key", key,
			"error", err)
		return err
	}

	return nil
}

// LastDM retrieves last DM time with Datastore-first, memory fallback.
func (s *DatastoreStore) LastDM(ctx context.Context, userID, prURL string) (time.Time, bool) {
	// Check memory first (fast)
	t, exists := s.memory.LastDM(ctx, userID, prURL)
	if exists {
		return t, true
	}

	// Datastore disabled
	if s.disabled || s.ds == nil {
		return time.Time{}, false
	}

	// Try Datastore
	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	key := dmKey(userID, prURL)
	dsKey := datastore.NameKey(kindDM, key, nil)
	var entity dmEntity

	err := s.ds.Get(timeoutCtx, dsKey, &entity)
	if err != nil {
		return time.Time{}, false
	}

	// Update memory cache
	if err := s.memory.RecordDM(ctx, userID, prURL, entity.SentAt); err != nil {
		slog.Debug("failed to update memory cache for DM", "error", err)
	}

	return entity.SentAt, true
}

// RecordDM saves DM timestamp to both stores.
func (s *DatastoreStore) RecordDM(ctx context.Context, userID, prURL string, sentAt time.Time) error {
	// Save to memory
	if err := s.memory.RecordDM(ctx, userID, prURL, sentAt); err != nil {
		slog.Warn("failed to record DM in memory", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Capture client for safe concurrent access
	ds := s.ds

	// Save to Datastore with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	key := dmKey(userID, prURL)
	dsKey := datastore.NameKey(kindDM, key, nil)
	entity := &dmEntity{
		UserID: userID,
		PRURL:  prURL,
		SentAt: sentAt,
	}

	if _, err := ds.Put(timeoutCtx, dsKey, entity); err != nil {
		slog.Error("failed to record DM in Datastore",
			"user", userID,
			"error", err)
		return err
	}

	return nil
}

// DMMessage retrieves DM message info with Datastore-first, memory fallback.
func (s *DatastoreStore) DMMessage(ctx context.Context, userID, prURL string) (DMInfo, bool) {
	// Check memory first (fast)
	info, exists := s.memory.DMMessage(ctx, userID, prURL)
	if exists {
		return info, true
	}

	// Datastore disabled
	if s.disabled || s.ds == nil {
		return DMInfo{}, false
	}

	// Try Datastore
	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	key := dmKey(userID, prURL)
	dsKey := datastore.NameKey(kindDMMessage, key, nil)
	var entity dmMessageEntity

	err := s.ds.Get(timeoutCtx, dsKey, &entity)
	if err != nil {
		return DMInfo{}, false
	}

	// Found in Datastore - update memory cache and return
	result := DMInfo(entity)

	// Update memory cache
	if err := s.memory.SaveDMMessage(ctx, userID, prURL, result); err != nil {
		slog.Debug("failed to update memory cache for DM message", "error", err)
	}

	return result, true
}

// SaveDMMessage saves DM message info to both stores.
func (s *DatastoreStore) SaveDMMessage(ctx context.Context, userID, prURL string, info DMInfo) error {
	// Always save to memory first (fast, local)
	if err := s.memory.SaveDMMessage(ctx, userID, prURL, info); err != nil {
		slog.Warn("failed to save DM message to memory", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Capture client for safe concurrent access
	ds := s.ds

	// Save to Datastore with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	key := dmKey(userID, prURL)
	dsKey := datastore.NameKey(kindDMMessage, key, nil)
	entity := &dmMessageEntity{
		ChannelID:   info.ChannelID,
		MessageTS:   info.MessageTS,
		MessageText: info.MessageText,
		UpdatedAt:   time.Now(),
		SentAt:      info.SentAt,
	}

	if _, err := ds.Put(timeoutCtx, dsKey, entity); err != nil {
		slog.Error("failed to save DM message to Datastore",
			"user", userID,
			"error", err)
		return err
	}

	return nil
}

// ListDMUsers returns all user IDs who have received DMs for a given PR.
// Queries both memory cache and Datastore to ensure data persists across restarts.
func (s *DatastoreStore) ListDMUsers(ctx context.Context, prURL string) []string {
	// Check memory cache first (fast path)
	users := s.memory.ListDMUsers(ctx, prURL)
	if len(users) > 0 || s.disabled || s.ds == nil {
		return users
	}

	// PERFORMANCE NOTE: Datastore queries are expensive and lack substring/regex filtering.
	// We must fetch all DM message keys and filter client-side. This is acceptable because:
	// 1. Uses KeysOnly() to minimize data transfer (no entity bodies)
	// 2. Bounded by reasonable limit (1000 - PRs rarely have >100 participants)
	// 3. Only runs on cache miss (typically at startup)
	// 4. Results populate memory cache for future fast lookups
	//
	// Alternative considered: Ancestor queries require schema change (breaking existing data)
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := datastore.NewQuery(kindDMMessage).KeysOnly().Limit(1000)
	keys, err := s.ds.AllKeys(timeoutCtx, query)
	if err != nil {
		slog.Warn("failed to query Datastore for DM users",
			"pr_url", prURL,
			"error", err,
			"fallback", "returning empty list")
		return nil
	}

	// Filter keys matching this PR URL
	// Key format: "dm:{userID}:{prURL}"
	suffix := ":" + prURL
	filtered := make([]string, 0, min(len(keys), 50)) // Most PRs have <50 DM recipients

	for _, key := range keys {
		if strings.HasSuffix(key.Name, suffix) {
			// Extract userID from key
			parts := strings.SplitN(key.Name, ":", 3)
			if len(parts) == 3 {
				filtered = append(filtered, parts[1])
			}
		}
	}

	slog.Debug("queried Datastore for DM users",
		"pr_url", prURL,
		"users_found", len(filtered),
		"keys_scanned", len(keys))

	return filtered
}

// LastDigest retrieves last digest time.
func (s *DatastoreStore) LastDigest(ctx context.Context, userID, date string) (time.Time, bool) {
	// Check memory first
	t, exists := s.memory.LastDigest(ctx, userID, date)
	if exists {
		return t, true
	}

	// Datastore disabled
	if s.disabled || s.ds == nil {
		return time.Time{}, false
	}

	// Try Datastore
	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	key := digestKey(userID, date)
	dsKey := datastore.NameKey(kindDigest, key, nil)
	var entity digestEntity

	err := s.ds.Get(timeoutCtx, dsKey, &entity)
	if err != nil {
		return time.Time{}, false
	}

	// Update cache
	if err := s.memory.RecordDigest(ctx, userID, date, entity.SentAt); err != nil {
		slog.Debug("failed to update memory cache for digest", "error", err)
	}

	return entity.SentAt, true
}

// RecordDigest saves digest timestamp to memory and attempts persistence to Datastore.
// Memory is always updated (primary storage for runtime). Datastore is best-effort for restart recovery.
// Degrades gracefully: logs errors but continues operating if Datastore unavailable.
func (s *DatastoreStore) RecordDigest(ctx context.Context, userID, date string, sentAt time.Time) error {
	// Always save to memory first (primary storage, must succeed)
	if err := s.memory.RecordDigest(ctx, userID, date, sentAt); err != nil {
		slog.Warn("failed to record digest in memory", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Best-effort persistence to Datastore for restart recovery
	// Synchronous write for maximum reliability, but don't fail operation if it doesn't work
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	key := digestKey(userID, date)
	dsKey := datastore.NameKey(kindDigest, key, nil)
	entity := &digestEntity{
		UserID: userID,
		Date:   date,
		SentAt: sentAt,
	}

	if _, err := s.ds.Put(timeoutCtx, dsKey, entity); err != nil {
		slog.Error("failed to persist digest to Datastore - may send duplicate after restart",
			"user", userID,
			"date", date,
			"error", err)
		// Graceful degradation: log error but don't fail the operation
		// System continues running even if external persistence unavailable
	}

	return nil
}

// WasProcessed checks if an event was already processed (distributed check).
func (s *DatastoreStore) WasProcessed(ctx context.Context, eventKey string) bool {
	// Check memory first (fast)
	if s.memory.WasProcessed(ctx, eventKey) {
		return true
	}

	// Datastore disabled
	if s.disabled || s.ds == nil {
		return false
	}

	// Check Datastore (cross-instance coordination)
	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	dsKey := datastore.NameKey(kindEvent, eventKey, nil)
	var entity eventEntity

	err := s.ds.Get(timeoutCtx, dsKey, &entity)
	exists := err == nil

	if exists {
		// Update local cache
		if err := s.memory.MarkProcessed(ctx, eventKey, 24*time.Hour); err != nil {
			slog.Debug("failed to update memory cache for event", "error", err)
		}
	}

	return exists
}

// MarkProcessed marks an event as processed (distributed coordination).
// Returns error if already processed by another instance (enables race detection).
func (s *DatastoreStore) MarkProcessed(ctx context.Context, eventKey string, ttl time.Duration) error {
	// Mark in memory first for fast local lookups
	if err := s.memory.MarkProcessed(ctx, eventKey, ttl); err != nil {
		slog.Warn("failed to mark event in memory", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Use transaction for compare-and-swap semantics
	// Timeout: 10 seconds for transaction (Google recommends up to 60s idle timeout)
	// This accounts for cold starts, network latency, and transaction overhead
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dsKey := datastore.NameKey(kindEvent, eventKey, nil)

	_, err := s.ds.RunInTransaction(timeoutCtx, func(tx *datastore.Transaction) error {
		var existing eventEntity
		err := tx.Get(dsKey, &existing)

		// Already exists - another instance processed it
		if err == nil {
			return ErrAlreadyProcessed
		}

		// Not found - safe to insert
		if errors.Is(err, datastore.ErrNoSuchEntity) {
			entity := &eventEntity{
				EventKey:  eventKey,
				Processed: time.Now(),
			}
			_, err = tx.Put(dsKey, entity)
			return err
		}

		// Other error
		return err
	})
	// Return error only for race conditions (ErrAlreadyProcessed)
	// For other errors, degrade gracefully
	if err != nil {
		if errors.Is(err, ErrAlreadyProcessed) {
			// This is expected during rolling deployments - return error to caller
			return err
		}
		// Unexpected error - log but don't fail processing (graceful degradation)
		slog.Error("failed to mark event in Datastore - continuing with memory-only tracking",
			"event", eventKey,
			"error", err)
		return nil
	}

	return nil
}

// LastNotification retrieves when a PR was last notified about.
func (s *DatastoreStore) LastNotification(ctx context.Context, prURL string) time.Time {
	// Datastore disabled
	if s.disabled || s.ds == nil {
		return time.Time{}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	dsKey := datastore.NameKey(kindNotify, prURL, nil)
	var entity notifyEntity

	err := s.ds.Get(timeoutCtx, dsKey, &entity)
	if err != nil {
		return time.Time{}
	}

	return entity.NotifiedAt
}

// RecordNotification records when we notified about a PR.
func (s *DatastoreStore) RecordNotification(ctx context.Context, prURL string, notifiedAt time.Time) error {
	// Skip if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Capture client for safe concurrent access
	ds := s.ds

	// Save with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	dsKey := datastore.NameKey(kindNotify, prURL, nil)
	entity := &notifyEntity{
		PRURL:      prURL,
		NotifiedAt: notifiedAt,
	}

	if _, err := ds.Put(timeoutCtx, dsKey, entity); err != nil {
		slog.Error("failed to record notification in Datastore", "error", err)
		return err
	}

	return nil
}

// QueuePendingDM adds a pending DM to both memory and Datastore.
func (s *DatastoreStore) QueuePendingDM(ctx context.Context, dm *PendingDM) error {
	// Always update memory cache
	if err := s.memory.QueuePendingDM(ctx, dm); err != nil {
		return err
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Use passed ctx parameter
	key := datastore.NameKey(kindPendingDM, dm.ID, nil)
	entity := pendingDMEntity{
		WorkspaceID:   dm.WorkspaceID,
		UserID:        dm.UserID,
		PROwner:       dm.PROwner,
		PRRepo:        dm.PRRepo,
		PRNumber:      dm.PRNumber,
		PRURL:         dm.PRURL,
		PRTitle:       dm.PRTitle,
		PRAuthor:      dm.PRAuthor,
		PRState:       dm.PRState,
		WorkflowState: dm.WorkflowState,
		NextActions:   dm.NextActions,
		ChannelID:     dm.ChannelID,
		ChannelName:   dm.ChannelName,
		QueuedAt:      dm.QueuedAt,
		SendAfter:     dm.SendAfter,
	}

	_, err := s.ds.Put(ctx, key, &entity)
	return err
}

// PendingDMs returns all pending DMs that should be sent.
// Reads from memory cache first, falls back to Datastore if empty.
func (s *DatastoreStore) PendingDMs(ctx context.Context, before time.Time) ([]PendingDM, error) {
	// Try memory first
	dms, err := s.memory.PendingDMs(ctx, before)
	if err == nil && len(dms) > 0 {
		return dms, nil
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return dms, nil
	}

	// Query Datastore for pending DMs
	// Use passed ctx parameter
	query := datastore.NewQuery(kindPendingDM).
		Filter("send_after <=", before).
		Limit(100)

	var entities []pendingDMEntity
	keys, err := s.ds.GetAll(ctx, query, &entities)
	if err != nil {
		slog.Warn("failed to query pending DMs from Datastore", "error", err)
		return dms, nil // Return memory results even if Datastore fails
	}

	// Convert entities to PendingDM structs and update memory cache
	result := make([]PendingDM, 0, len(entities))
	for i := range entities {
		entity := &entities[i]
		dm := PendingDM{
			ID:            keys[i].Name,
			WorkspaceID:   entity.WorkspaceID,
			UserID:        entity.UserID,
			PROwner:       entity.PROwner,
			PRRepo:        entity.PRRepo,
			PRNumber:      entity.PRNumber,
			PRURL:         entity.PRURL,
			PRTitle:       entity.PRTitle,
			PRAuthor:      entity.PRAuthor,
			PRState:       entity.PRState,
			WorkflowState: entity.WorkflowState,
			NextActions:   entity.NextActions,
			ChannelID:     entity.ChannelID,
			ChannelName:   entity.ChannelName,
			QueuedAt:      entity.QueuedAt,
			SendAfter:     entity.SendAfter,
		}
		result = append(result, dm)
		// Update memory cache (ignore error since cache is best-effort and we have authoritative result from datastore)
		//nolint:errcheck // Cache update is best-effort; authoritative result from datastore
		_ = s.memory.QueuePendingDM(ctx, &dm)
	}

	return result, nil
}

// RemovePendingDM removes a pending DM from both memory and Datastore.
func (s *DatastoreStore) RemovePendingDM(ctx context.Context, id string) error {
	// Always remove from memory
	if err := s.memory.RemovePendingDM(ctx, id); err != nil {
		return err
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Use passed ctx parameter
	key := datastore.NameKey(kindPendingDM, id, nil)
	return s.ds.Delete(ctx, key)
}

// Cleanup removes old data from both stores.
func (s *DatastoreStore) Cleanup(ctx context.Context) error {
	// Always cleanup memory
	if err := s.memory.Cleanup(ctx); err != nil {
		slog.Warn("memory cleanup failed", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Datastore cleanup is done async via TTL or manual queries
	// For now, rely on memory cleanup and Datastore's natural expiration
	return nil
}

// Close releases resources.
func (s *DatastoreStore) Close() error {
	if s.memory != nil {
		if err := s.memory.Close(); err != nil {
			slog.Warn("failed to close memory store", "error", err)
		}
	}

	if s.ds != nil && !s.disabled {
		if err := s.ds.Close(); err != nil {
			slog.Warn("failed to close Datastore client", "error", err)
			return err
		}
	}

	return nil
}
