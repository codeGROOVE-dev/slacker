// Package state provides persistent storage for bot state across restarts.
package state

import (
	"time"
)

// ThreadInfo stores information about a Slack thread for a PR.
type ThreadInfo struct {
	UpdatedAt     time.Time `json:"updated_at"`
	LastEventTime time.Time `json:"last_event_time"` // Last sprinkler event timestamp for turnclient cache optimization
	ThreadTS      string    `json:"thread_ts"`
	ChannelID     string    `json:"channel_id"`
	LastState     string    `json:"last_state"`
	MessageText   string    `json:"message_text"`
}

// DMInfo stores information about a DM message for a PR.
type DMInfo struct {
	UpdatedAt   time.Time `json:"updated_at"`   // When we last updated this message
	SentAt      time.Time `json:"sent_at"`      // When we first sent this message
	ChannelID   string    `json:"channel_id"`   // DM conversation channel ID
	MessageTS   string    `json:"message_ts"`   // Message timestamp for updating
	MessageText string    `json:"message_text"` // Current message text
}

// Store provides persistent storage for bot state.
// Implementations must be safe for concurrent use.
//
//nolint:interfacebloat // Store intentionally groups all state operations for simplicity
type Store interface {
	// Thread operations - map PR to Slack thread
	Thread(owner, repo string, number int, channelID string) (ThreadInfo, bool)
	SaveThread(owner, repo string, number int, channelID string, info ThreadInfo) error

	// DM tracking - prevent duplicate notifications
	LastDM(userID, prURL string) (time.Time, bool)
	RecordDM(userID, prURL string, sentAt time.Time) error

	// DM message tracking - store DM message info for updating
	DMMessage(userID, prURL string) (DMInfo, bool)
	SaveDMMessage(userID, prURL string, info DMInfo) error
	ListDMUsers(prURL string) []string

	// Daily digest tracking - one per user per day
	LastDigest(userID, date string) (time.Time, bool)
	RecordDigest(userID, date string, sentAt time.Time) error

	// Event deduplication - prevent processing same event twice
	WasProcessed(eventKey string) bool
	MarkProcessed(eventKey string, ttl time.Duration) error

	// Notification tracking - track when we last notified about a PR
	LastNotification(prURL string) time.Time
	RecordNotification(prURL string, notifiedAt time.Time) error

	// Cleanup old data
	Cleanup() error

	// Close releases resources
	Close() error
}
