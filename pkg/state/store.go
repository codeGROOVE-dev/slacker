// Package state provides persistent storage for bot state across restarts.
package state

import (
	"context"
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
	LastState   string    `json:"last_state"`   // Last PR state we notified about (for idempotency)
}

// PendingDM represents a DM scheduled to be sent later.
type PendingDM struct {
	QueuedAt      time.Time `json:"queued_at"`
	SendAfter     time.Time `json:"send_after"`
	PRAuthor      string    `json:"pr_author"`
	PRState       string    `json:"pr_state"`
	PRRepo        string    `json:"pr_repo"`
	WorkspaceID   string    `json:"workspace_id"`
	PRURL         string    `json:"pr_url"`
	PRTitle       string    `json:"pr_title"`
	ID            string    `json:"id"`
	PROwner       string    `json:"pr_owner"`
	WorkflowState string    `json:"workflow_state"`
	NextActions   string    `json:"next_actions"`
	ChannelID     string    `json:"channel_id"`
	ChannelName   string    `json:"channel_name"`
	UserID        string    `json:"user_id"`
	PRNumber      int       `json:"pr_number"`
}

// Store provides persistent storage for bot state.
// Implementations must be safe for concurrent use.
//
//nolint:interfacebloat // Store intentionally groups all state operations for simplicity
type Store interface {
	// Thread operations - map PR to Slack thread
	Thread(ctx context.Context, owner, repo string, number int, channelID string) (ThreadInfo, bool)
	SaveThread(ctx context.Context, owner, repo string, number int, channelID string, info ThreadInfo) error

	// DM tracking - prevent duplicate notifications
	LastDM(ctx context.Context, userID, prURL string) (time.Time, bool)
	RecordDM(ctx context.Context, userID, prURL string, sentAt time.Time) error

	// DM message tracking - store DM message info for updating
	DMMessage(ctx context.Context, userID, prURL string) (DMInfo, bool)
	SaveDMMessage(ctx context.Context, userID, prURL string, info DMInfo) error
	ListDMUsers(ctx context.Context, prURL string) []string

	// Daily digest tracking - one per user per day
	LastDigest(ctx context.Context, userID, date string) (time.Time, bool)
	RecordDigest(ctx context.Context, userID, date string, sentAt time.Time) error

	// Event deduplication - prevent processing same event twice
	WasProcessed(ctx context.Context, eventKey string) bool
	MarkProcessed(ctx context.Context, eventKey string, ttl time.Duration) error

	// Notification tracking - track when we last notified about a PR
	LastNotification(ctx context.Context, prURL string) time.Time
	RecordNotification(ctx context.Context, prURL string, notifiedAt time.Time) error

	// Pending DM queue - schedule DMs to be sent later
	QueuePendingDM(ctx context.Context, dm *PendingDM) error
	PendingDMs(ctx context.Context, before time.Time) ([]PendingDM, error)
	RemovePendingDM(ctx context.Context, id string) error

	// Cleanup old data
	Cleanup(ctx context.Context) error

	// Close releases resources
	Close() error
}
