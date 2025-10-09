// Package notify handles notification scheduling and delivery.
package notify

import (
	"strconv"
	"sync"
	"time"
)

// NotificationTracker tracks notification timestamps in memory.
// Rate limiting state resets on service restart, which is acceptable.
type NotificationTracker struct {
	mu sync.RWMutex
	// Key: "workspaceID:userID"
	lastDM    map[string]time.Time
	lastDaily map[string]time.Time
	// Key: "workspaceID:owner/repo#123"
	lastChannelNotification map[string]time.Time
	// Key: "workspaceID:userID:owner/repo#123" -> TagInfo with channel and timestamp
	lastUserPRChannelTag map[string]TagInfo
}

// TagInfo stores when and where a user was tagged for a PR.
type TagInfo struct {
	Timestamp time.Time
	ChannelID string
}

// LastDMNotification returns when a user was last DM'd.
func (t *NotificationTracker) LastDMNotification(workspaceID, userID string) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	key := workspaceID + ":" + userID
	return t.lastDM[key]
}

// UpdateDMNotification records that a user was just DM'd.
func (t *NotificationTracker) UpdateDMNotification(workspaceID, userID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := workspaceID + ":" + userID
	t.lastDM[key] = time.Now()
}

// LastDailyReminder returns when a user last received a daily reminder.
func (t *NotificationTracker) LastDailyReminder(workspaceID, userID string) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	key := workspaceID + ":" + userID
	return t.lastDaily[key]
}

// UpdateDailyReminder records that a user just received a daily reminder.
func (t *NotificationTracker) UpdateDailyReminder(workspaceID, userID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := workspaceID + ":" + userID
	t.lastDaily[key] = time.Now()
}

// LastChannelNotification returns when a PR was last mentioned in a channel.
func (t *NotificationTracker) LastChannelNotification(workspaceID, owner, repo string, number int) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	key := workspaceID + ":" + owner + "/" + repo + "#" + strconv.Itoa(number)
	return t.lastChannelNotification[key]
}

// UpdateChannelNotification records that a PR was just mentioned in a channel.
func (t *NotificationTracker) UpdateChannelNotification(workspaceID, owner, repo string, number int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := workspaceID + ":" + owner + "/" + repo + "#" + strconv.Itoa(number)
	t.lastChannelNotification[key] = time.Now()
}

// LastUserPRChannelTag returns tag info (timestamp and channel) for when a user was last tagged for a specific PR.
func (t *NotificationTracker) LastUserPRChannelTag(workspaceID, userID, owner, repo string, number int) TagInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	key := workspaceID + ":" + userID + ":" + owner + "/" + repo + "#" + strconv.Itoa(number)
	return t.lastUserPRChannelTag[key]
}

// UpdateUserPRChannelTag records that a user was just tagged in a channel for a specific PR.
// If called multiple times for same user/PR, only keeps the FIRST tag (earliest channel).
func (t *NotificationTracker) UpdateUserPRChannelTag(workspaceID, userID, channelID, owner, repo string, number int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := workspaceID + ":" + userID + ":" + owner + "/" + repo + "#" + strconv.Itoa(number)

	// Only update if not already tagged (keep first channel where user was tagged)
	if _, exists := t.lastUserPRChannelTag[key]; !exists {
		t.lastUserPRChannelTag[key] = TagInfo{
			Timestamp: time.Now(),
			ChannelID: channelID,
		}
	}
}
