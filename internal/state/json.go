package state

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JSONStore implements Store using JSON files in the user cache directory.
// Simple, reliable, and easy to debug.
type JSONStore struct {
	baseDir string
	mu      sync.RWMutex

	// In-memory cache for fast lookups
	threads       map[string]ThreadInfo
	dms           map[string]time.Time
	digests       map[string]time.Time
	events        map[string]time.Time
	notifications map[string]time.Time
	modified      bool // Track if we need to save
}

// NewJSONStore creates a new JSON-based state store.
// Uses os.UserCacheDir() + /slacker/state/ for storage.
func NewJSONStore() (*JSONStore, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		// Fallback to temp dir if cache dir not available
		cacheDir = os.TempDir()
		slog.Warn("using temp dir instead of cache dir", "path", cacheDir, "error", err)
	}

	baseDir := filepath.Join(cacheDir, "slacker", "state")

	store := &JSONStore{
		baseDir:       baseDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
		modified:      false,
	}

	// Create directory structure
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	// Load existing state
	if err := store.load(); err != nil {
		slog.Warn("failed to load state, starting fresh", "error", err)
	}

	slog.Info("initialized JSON state store", "path", baseDir)
	return store, nil
}

// Thread key format: "{owner}/{repo}#{number}:{channel_id}".
func threadKey(owner, repo string, number int, channelID string) string {
	return fmt.Sprintf("%s/%s#%d:%s", owner, repo, number, channelID)
}

// DM key format: "dm:{user_id}:{pr_url}".
func dmKey(userID, prURL string) string {
	return fmt.Sprintf("dm:%s:%s", userID, prURL)
}

// Digest key format: "digest:{user_id}:{date}".
func digestKey(userID, date string) string {
	return fmt.Sprintf("digest:%s:%s", userID, date)
}

// GetThread retrieves thread information for a PR.
func (s *JSONStore) GetThread(owner, repo string, number int, channelID string) (ThreadInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := threadKey(owner, repo, number, channelID)
	info, exists := s.threads[key]
	return info, exists
}

// SaveThread saves thread information for a PR.
func (s *JSONStore) SaveThread(owner, repo string, number int, channelID string, info ThreadInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := threadKey(owner, repo, number, channelID)
	info.UpdatedAt = time.Now()
	s.threads[key] = info
	s.modified = true
	return s.save()
}

// GetLastDM retrieves the last DM timestamp for a user and PR.
func (s *JSONStore) GetLastDM(userID, prURL string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := dmKey(userID, prURL)
	t, exists := s.dms[key]
	return t, exists
}

// RecordDM records when a DM was sent to a user about a PR.
func (s *JSONStore) RecordDM(userID, prURL string, sentAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dmKey(userID, prURL)
	s.dms[key] = sentAt
	s.modified = true
	return s.save()
}

// GetLastDigest retrieves the last digest timestamp for a user and date.
func (s *JSONStore) GetLastDigest(userID, date string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := digestKey(userID, date)
	t, exists := s.digests[key]
	return t, exists
}

// RecordDigest records when a digest was sent to a user.
func (s *JSONStore) RecordDigest(userID, date string, sentAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := digestKey(userID, date)
	s.digests[key] = sentAt
	s.modified = true
	return s.save()
}

// WasProcessed checks if an event was already processed.
func (s *JSONStore) WasProcessed(eventKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.events[eventKey]
	return exists
}

// MarkProcessed marks an event as processed with an optional TTL.
// Note: TTL is currently ignored - cleanup uses hardcoded 24-hour retention.
// This could be enhanced in the future to support per-event TTL.
func (s *JSONStore) MarkProcessed(eventKey string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[eventKey] = time.Now()
	s.modified = true
	return s.save()
}

// GetLastNotification retrieves the last notification timestamp for a PR.
func (s *JSONStore) GetLastNotification(prURL string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notifications[prURL]
}

// RecordNotification records when a notification was sent for a PR.
func (s *JSONStore) RecordNotification(prURL string, notifiedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifications[prURL] = notifiedAt
	s.modified = true
	return s.save()
}

// Cleanup removes old data from all maps.
func (s *JSONStore) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cleanedThreads := 0
	cleanedDMs := 0
	cleanedDigests := 0
	cleanedEvents := 0

	// Clean up old threads (>30 days)
	for key, info := range s.threads {
		if now.Sub(info.UpdatedAt) > 30*24*time.Hour {
			delete(s.threads, key)
			cleanedThreads++
		}
	}

	// Clean up old DMs (>90 days)
	for key, t := range s.dms {
		if now.Sub(t) > 90*24*time.Hour {
			delete(s.dms, key)
			cleanedDMs++
		}
	}

	// Clean up old digests (>30 days)
	for key, t := range s.digests {
		if now.Sub(t) > 30*24*time.Hour {
			delete(s.digests, key)
			cleanedDigests++
		}
	}

	// Clean up old events (>24 hours)
	for key, t := range s.events {
		if now.Sub(t) > 24*time.Hour {
			delete(s.events, key)
			cleanedEvents++
		}
	}

	if cleanedThreads+cleanedDMs+cleanedDigests+cleanedEvents > 0 {
		slog.Info("cleaned up old state",
			"threads", cleanedThreads,
			"dms", cleanedDMs,
			"digests", cleanedDigests,
			"events", cleanedEvents)
		s.modified = true
		return s.save()
	}

	return nil
}

// Close saves any pending changes and releases resources.
func (s *JSONStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modified {
		return s.save()
	}
	return nil
}

// Persistent storage format.
type persistentState struct {
	Threads       map[string]ThreadInfo `json:"threads"`
	DMs           map[string]time.Time  `json:"dms"`
	Digests       map[string]time.Time  `json:"digests"`
	Events        map[string]time.Time  `json:"events"`
	Notifications map[string]time.Time  `json:"notifications"`
}

// save persists state to disk.
// Must be called with lock held.
func (s *JSONStore) save() error {
	if !s.modified {
		return nil
	}

	state := persistentState{
		Threads:       s.threads,
		DMs:           s.dms,
		Digests:       s.digests,
		Events:        s.events,
		Notifications: s.notifications,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	filePath := filepath.Join(s.baseDir, "state.json")
	tmpFile := filePath + ".tmp"

	// Write to temp file first
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpFile, filePath); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	s.modified = false
	return nil
}

// load reads state from disk.
// Must be called with lock held.
func (s *JSONStore) load() error {
	stateFile := filepath.Join(s.baseDir, "state.json")

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("no existing state file, starting fresh")
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var state persistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	s.threads = state.Threads
	s.dms = state.DMs
	s.digests = state.Digests
	s.events = state.Events
	s.notifications = state.Notifications

	if s.threads == nil {
		s.threads = make(map[string]ThreadInfo)
	}
	if s.dms == nil {
		s.dms = make(map[string]time.Time)
	}
	if s.digests == nil {
		s.digests = make(map[string]time.Time)
	}
	if s.events == nil {
		s.events = make(map[string]time.Time)
	}
	if s.notifications == nil {
		s.notifications = make(map[string]time.Time)
	}

	slog.Info("loaded state from disk",
		"threads", len(s.threads),
		"dms", len(s.dms),
		"digests", len(s.digests),
		"events", len(s.events),
		"notifications", len(s.notifications))

	return nil
}
