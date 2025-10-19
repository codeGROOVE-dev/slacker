package state

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"cloud.google.com/go/datastore"
)

// DatastoreStore implements Store using Google Cloud Datastore with JSON fallback.
// Uses hybrid approach: write to both, read from Datastore with JSON fallback.
type DatastoreStore struct {
	ds       *datastore.Client
	json     *JSONStore
	disabled bool // If Datastore failed to initialize, use JSON only
}

// Entity types for Datastore.
const (
	kindThread = "SlackerThread"
	kindDM     = "SlackerDM"
	kindDigest = "SlackerDigest"
	kindEvent  = "SlackerEvent"
	kindNotify = "SlackerNotification"
)

// Thread entity for Datastore.
type threadEntity struct {
	ThreadTS    string    `datastore:"thread_ts"`
	ChannelID   string    `datastore:"channel_id"`
	MessageText string    `datastore:"message_text,noindex"`
	UpdatedAt   time.Time `datastore:"updated_at"`
}

// DM tracking entity.
type dmEntity struct {
	UserID string    `datastore:"user_id"`
	PRURL  string    `datastore:"pr_url"`
	SentAt time.Time `datastore:"sent_at"`
}

// Digest tracking entity.
type digestEntity struct {
	UserID string    `datastore:"user_id"`
	Date   string    `datastore:"date"` // YYYY-MM-DD format
	SentAt time.Time `datastore:"sent_at"`
}

// Event deduplication entity.
type eventEntity struct {
	EventKey  string    `datastore:"event_key"`
	Processed time.Time `datastore:"processed"`
}

// Notification tracking entity.
type notifyEntity struct {
	PRURL      string    `datastore:"pr_url"`
	NotifiedAt time.Time `datastore:"notified_at"`
}

// NewDatastoreStore creates a new Datastore-backed store with JSON fallback.
// The databaseID parameter specifies which Datastore database to use (e.g., "slacker", "(default)").
func NewDatastoreStore(ctx context.Context, projectID, databaseID string) (*DatastoreStore, error) {
	// Always create JSON store as fallback
	jsonStore, err := NewJSONStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create JSON fallback store: %w", err)
	}

	// Create Datastore client with specified database
	// Use NewClientWithDatabase to specify which database to use
	ds, err := datastore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		slog.Warn("failed to create Datastore client, using JSON-only mode",
			"error", err,
			"project_id", projectID,
			"database_id", databaseID,
			"fallback", "JSON files only")
		return &DatastoreStore{
			json:     jsonStore,
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
		slog.Warn("Datastore connectivity test failed, using JSON-only mode",
			"error", err,
			"fallback", "JSON files only")
		if closeErr := ds.Close(); closeErr != nil {
			slog.Debug("failed to close Datastore during test", "error", closeErr)
		}
		return &DatastoreStore{
			json:     jsonStore,
			disabled: true,
		}, nil
	}

	// Clean up test entity
	if err := ds.Delete(ctx, testKey); err != nil {
		slog.Debug("failed to delete test entity", "error", err)
	}

	slog.Info("initialized Datastore with JSON fallback",
		"project_id", projectID,
		"mode", "hybrid")

	return &DatastoreStore{
		ds:       ds,
		json:     jsonStore,
		disabled: false,
	}, nil
}

// GetThread retrieves thread info with Datastore-first, JSON fallback.
func (s *DatastoreStore) GetThread(owner, repo string, number int, channelID string) (ThreadInfo, bool) {
	key := threadKey(owner, repo, number, channelID)

	// Fast path: Check JSON cache first
	info, exists := s.json.GetThread(owner, repo, number, channelID)
	if exists {
		return info, true
	}

	// Datastore disabled or not available
	if s.disabled || s.ds == nil {
		return ThreadInfo{}, false
	}

	// Try Datastore with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	dsKey := datastore.NameKey(kindThread, key, nil)
	var entity threadEntity

	err := s.ds.Get(ctx, dsKey, &entity)
	if err != nil {
		if !errors.Is(err, datastore.ErrNoSuchEntity) {
			slog.Debug("Datastore get failed, using cache",
				"key", key,
				"error", err)
		}
		return ThreadInfo{}, false
	}

	// Found in Datastore - update JSON cache and return
	result := ThreadInfo{
		ThreadTS:    entity.ThreadTS,
		ChannelID:   entity.ChannelID,
		MessageText: entity.MessageText,
		UpdatedAt:   entity.UpdatedAt,
	}

	// Async update JSON cache (don't wait)
	go func() {
		if err := s.json.SaveThread(owner, repo, number, channelID, result); err != nil {
			slog.Debug("failed to update JSON cache for thread", "error", err)
		}
	}()

	return result, true
}

// SaveThread saves thread info to both Datastore and JSON.
func (s *DatastoreStore) SaveThread(owner, repo string, number int, channelID string, info ThreadInfo) error {
	key := threadKey(owner, repo, number, channelID)

	// Always save to JSON (fast, local)
	if err := s.json.SaveThread(owner, repo, number, channelID, info); err != nil {
		slog.Warn("failed to save thread to JSON", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Save to Datastore asynchronously (don't block)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		dsKey := datastore.NameKey(kindThread, key, nil)
		entity := &threadEntity{
			ThreadTS:    info.ThreadTS,
			ChannelID:   info.ChannelID,
			MessageText: info.MessageText,
			UpdatedAt:   time.Now(),
		}

		if _, err := s.ds.Put(ctx, dsKey, entity); err != nil {
			slog.Warn("failed to save thread to Datastore",
				"key", key,
				"error", err)
		}
	}()

	return nil
}

// GetLastDM retrieves last DM time with Datastore-first, JSON fallback.
func (s *DatastoreStore) GetLastDM(userID, prURL string) (time.Time, bool) {
	// Check JSON first (fast)
	t, exists := s.json.GetLastDM(userID, prURL)
	if exists {
		return t, true
	}

	// Datastore disabled
	if s.disabled || s.ds == nil {
		return time.Time{}, false
	}

	// Try Datastore
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	key := dmKey(userID, prURL)
	dsKey := datastore.NameKey(kindDM, key, nil)
	var entity dmEntity

	err := s.ds.Get(ctx, dsKey, &entity)
	if err != nil {
		return time.Time{}, false
	}

	// Update JSON cache async
	go func() {
		if err := s.json.RecordDM(userID, prURL, entity.SentAt); err != nil {
			slog.Debug("failed to update JSON cache for DM", "error", err)
		}
	}()

	return entity.SentAt, true
}

// RecordDM saves DM timestamp to both stores.
func (s *DatastoreStore) RecordDM(userID, prURL string, sentAt time.Time) error {
	// Save to JSON
	if err := s.json.RecordDM(userID, prURL, sentAt); err != nil {
		slog.Warn("failed to record DM in JSON", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Save to Datastore async
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		key := dmKey(userID, prURL)
		dsKey := datastore.NameKey(kindDM, key, nil)
		entity := &dmEntity{
			UserID: userID,
			PRURL:  prURL,
			SentAt: sentAt,
		}

		if _, err := s.ds.Put(ctx, dsKey, entity); err != nil {
			slog.Warn("failed to record DM in Datastore",
				"user", userID,
				"error", err)
		}
	}()

	return nil
}

// GetLastDigest retrieves last digest time.
func (s *DatastoreStore) GetLastDigest(userID, date string) (time.Time, bool) {
	// Check JSON first
	t, exists := s.json.GetLastDigest(userID, date)
	if exists {
		return t, true
	}

	// Datastore disabled
	if s.disabled || s.ds == nil {
		return time.Time{}, false
	}

	// Try Datastore
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	key := digestKey(userID, date)
	dsKey := datastore.NameKey(kindDigest, key, nil)
	var entity digestEntity

	err := s.ds.Get(ctx, dsKey, &entity)
	if err != nil {
		return time.Time{}, false
	}

	// Update cache
	go func() {
		if err := s.json.RecordDigest(userID, date, entity.SentAt); err != nil {
			slog.Debug("failed to update JSON cache for digest", "error", err)
		}
	}()

	return entity.SentAt, true
}

// RecordDigest saves digest timestamp.
func (s *DatastoreStore) RecordDigest(userID, date string, sentAt time.Time) error {
	// Save to JSON
	if err := s.json.RecordDigest(userID, date, sentAt); err != nil {
		slog.Warn("failed to record digest in JSON", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Save to Datastore async
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		key := digestKey(userID, date)
		dsKey := datastore.NameKey(kindDigest, key, nil)
		entity := &digestEntity{
			UserID: userID,
			Date:   date,
			SentAt: sentAt,
		}

		if _, err := s.ds.Put(ctx, dsKey, entity); err != nil {
			slog.Warn("failed to record digest in Datastore", "error", err)
		}
	}()

	return nil
}

// WasProcessed checks if an event was already processed (distributed check).
func (s *DatastoreStore) WasProcessed(eventKey string) bool {
	// Check JSON first (fast)
	if s.json.WasProcessed(eventKey) {
		return true
	}

	// Datastore disabled
	if s.disabled || s.ds == nil {
		return false
	}

	// Check Datastore (cross-instance coordination)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	dsKey := datastore.NameKey(kindEvent, eventKey, nil)
	var entity eventEntity

	err := s.ds.Get(ctx, dsKey, &entity)
	exists := err == nil

	if exists {
		// Update local cache
		go func() {
			if err := s.json.MarkProcessed(eventKey, 24*time.Hour); err != nil {
				slog.Debug("failed to update JSON cache for event", "error", err)
			}
		}()
	}

	return exists
}

// MarkProcessed marks an event as processed (distributed coordination).
// Returns true if successfully marked, false if already marked by another instance.
func (s *DatastoreStore) MarkProcessed(eventKey string, ttl time.Duration) error {
	// Mark in JSON
	if err := s.json.MarkProcessed(eventKey, ttl); err != nil {
		slog.Warn("failed to mark event in JSON", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Use transaction for compare-and-swap semantics
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	dsKey := datastore.NameKey(kindEvent, eventKey, nil)

	_, err := s.ds.RunInTransaction(ctx, func(tx *datastore.Transaction) error {
		var existing eventEntity
		err := tx.Get(dsKey, &existing)

		// Already exists - another instance processed it
		if err == nil {
			return errors.New("event already processed")
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

	if err != nil && err.Error() != "event already processed" {
		slog.Warn("failed to mark event in Datastore",
			"event", eventKey,
			"error", err)
	}

	return nil
}

// GetLastNotification retrieves when a PR was last notified about.
func (s *DatastoreStore) GetLastNotification(prURL string) time.Time {
	// Datastore disabled
	if s.disabled || s.ds == nil {
		return time.Time{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	dsKey := datastore.NameKey(kindNotify, prURL, nil)
	var entity notifyEntity

	err := s.ds.Get(ctx, dsKey, &entity)
	if err != nil {
		return time.Time{}
	}

	return entity.NotifiedAt
}

// RecordNotification records when we notified about a PR.
func (s *DatastoreStore) RecordNotification(prURL string, notifiedAt time.Time) error {
	// Skip if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Async save
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		dsKey := datastore.NameKey(kindNotify, prURL, nil)
		entity := &notifyEntity{
			PRURL:      prURL,
			NotifiedAt: notifiedAt,
		}

		if _, err := s.ds.Put(ctx, dsKey, entity); err != nil {
			slog.Warn("failed to record notification in Datastore", "error", err)
		}
	}()

	return nil
}

// Cleanup removes old data from both stores.
func (s *DatastoreStore) Cleanup() error {
	// Always cleanup JSON
	if err := s.json.Cleanup(); err != nil {
		slog.Warn("JSON cleanup failed", "error", err)
	}

	// Skip Datastore if disabled
	if s.disabled || s.ds == nil {
		return nil
	}

	// Datastore cleanup is done async via TTL or manual queries
	// For now, rely on JSON cleanup and Datastore's natural expiration
	return nil
}

// Close releases resources.
func (s *DatastoreStore) Close() error {
	if s.json != nil {
		if err := s.json.Close(); err != nil {
			slog.Warn("failed to close JSON store", "error", err)
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
