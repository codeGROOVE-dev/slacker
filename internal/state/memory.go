package state

import (
	"strings"
	"sync"
	"time"
)

// MemoryStore implements Store using in-memory maps only (no persistence).
// This is used as a cache layer on top of Datastore or JSON backends.
type MemoryStore struct {
	mu sync.RWMutex

	// In-memory cache for fast lookups
	threads       map[string]ThreadInfo
	dms           map[string]time.Time
	dmMessages    map[string]DMInfo
	digests       map[string]time.Time
	events        map[string]time.Time
	notifications map[string]time.Time
}

// NewMemoryStore creates a new in-memory-only state store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
	}
}

// Thread retrieves thread information for a PR.
func (s *MemoryStore) Thread(owner, repo string, number int, channelID string) (ThreadInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := threadKey(owner, repo, number, channelID)
	info, exists := s.threads[key]
	return info, exists
}

// SaveThread saves thread information for a PR.
func (s *MemoryStore) SaveThread(owner, repo string, number int, channelID string, info ThreadInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := threadKey(owner, repo, number, channelID)
	info.UpdatedAt = time.Now()
	s.threads[key] = info
	return nil
}

// LastDM retrieves the last DM timestamp for a user and PR.
func (s *MemoryStore) LastDM(userID, prURL string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := dmKey(userID, prURL)
	t, exists := s.dms[key]
	return t, exists
}

// RecordDM records when a DM was sent to a user about a PR.
func (s *MemoryStore) RecordDM(userID, prURL string, sentAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dmKey(userID, prURL)
	s.dms[key] = sentAt
	return nil
}

// DMMessage retrieves DM message information for a user and PR.
func (s *MemoryStore) DMMessage(userID, prURL string) (DMInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := dmKey(userID, prURL)
	info, exists := s.dmMessages[key]
	return info, exists
}

// SaveDMMessage saves DM message information for a user and PR.
func (s *MemoryStore) SaveDMMessage(userID, prURL string, info DMInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := dmKey(userID, prURL)
	info.UpdatedAt = time.Now()
	s.dmMessages[key] = info
	return nil
}

// ListDMUsers returns all user IDs who have received DMs for a given PR.
func (s *MemoryStore) ListDMUsers(prURL string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var users []string
	suffix := ":" + prURL

	for key := range s.dmMessages {
		if strings.HasSuffix(key, suffix) {
			// Extract userID from key format "dm:{userID}:{prURL}"
			parts := strings.SplitN(key, ":", 3)
			if len(parts) == 3 {
				users = append(users, parts[1])
			}
		}
	}

	return users
}

// LastDigest retrieves the last digest timestamp for a user and date.
func (s *MemoryStore) LastDigest(userID, date string) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := digestKey(userID, date)
	t, exists := s.digests[key]
	return t, exists
}

// RecordDigest records when a digest was sent to a user.
func (s *MemoryStore) RecordDigest(userID, date string, sentAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := digestKey(userID, date)
	s.digests[key] = sentAt
	return nil
}

// WasProcessed checks if an event was already processed.
func (s *MemoryStore) WasProcessed(eventKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.events[eventKey]
	return exists
}

// MarkProcessed marks an event as processed.
func (s *MemoryStore) MarkProcessed(eventKey string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[eventKey] = time.Now()
	return nil
}

// LastNotification retrieves the last notification timestamp for a PR.
func (s *MemoryStore) LastNotification(prURL string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notifications[prURL]
}

// RecordNotification records when a notification was sent for a PR.
func (s *MemoryStore) RecordNotification(prURL string, notifiedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifications[prURL] = notifiedAt
	return nil
}

// Cleanup removes old data from memory.
func (s *MemoryStore) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Clean up old threads (>30 days)
	for key, info := range s.threads {
		if now.Sub(info.UpdatedAt) > 30*24*time.Hour {
			delete(s.threads, key)
		}
	}

	// Clean up old DMs (>90 days)
	for key, t := range s.dms {
		if now.Sub(t) > 90*24*time.Hour {
			delete(s.dms, key)
		}
	}

	// Clean up old DM messages (>90 days)
	for key, info := range s.dmMessages {
		if now.Sub(info.UpdatedAt) > 90*24*time.Hour {
			delete(s.dmMessages, key)
		}
	}

	// Clean up old digests (>30 days)
	for key, t := range s.digests {
		if now.Sub(t) > 30*24*time.Hour {
			delete(s.digests, key)
		}
	}

	// Clean up old events (>24 hours)
	for key, t := range s.events {
		if now.Sub(t) > 24*time.Hour {
			delete(s.events, key)
		}
	}

	return nil
}

// Close is a no-op for in-memory store.
func (*MemoryStore) Close() error {
	return nil
}
