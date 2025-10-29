package notify

import (
	"sync"
	"testing"
	"time"
)

func TestNotificationTracker_DMNotifications(t *testing.T) {
	tracker := &NotificationTracker{
		lastDM:                  make(map[string]time.Time),
		lastDaily:               make(map[string]time.Time),
		lastChannelNotification: make(map[string]time.Time),
		lastUserPRChannelTag:    make(map[string]TagInfo),
	}

	// Test initial state - no notification recorded
	lastDM := tracker.LastDMNotification("workspace1", "U001")
	if !lastDM.IsZero() {
		t.Errorf("expected zero time for untracked user, got %v", lastDM)
	}

	// Update notification timestamp
	tracker.UpdateDMNotification("workspace1", "U001")

	// Verify it was recorded
	lastDM = tracker.LastDMNotification("workspace1", "U001")
	if lastDM.IsZero() {
		t.Error("expected non-zero time after update")
	}

	// Verify it's recent (within last second)
	if time.Since(lastDM) > time.Second {
		t.Errorf("expected recent timestamp, got %v", lastDM)
	}

	// Verify different workspace is separate
	lastDM2 := tracker.LastDMNotification("workspace2", "U001")
	if !lastDM2.IsZero() {
		t.Error("expected zero time for different workspace")
	}

	// Verify different user is separate
	lastDM3 := tracker.LastDMNotification("workspace1", "U002")
	if !lastDM3.IsZero() {
		t.Error("expected zero time for different user")
	}
}

func TestNotificationTracker_DailyReminders(t *testing.T) {
	tracker := &NotificationTracker{
		lastDM:                  make(map[string]time.Time),
		lastDaily:               make(map[string]time.Time),
		lastChannelNotification: make(map[string]time.Time),
		lastUserPRChannelTag:    make(map[string]TagInfo),
	}

	// Test initial state
	lastDaily := tracker.LastDailyReminder("workspace1", "U001")
	if !lastDaily.IsZero() {
		t.Errorf("expected zero time for untracked user, got %v", lastDaily)
	}

	// Update reminder timestamp
	tracker.UpdateDailyReminder("workspace1", "U001")

	// Verify it was recorded
	lastDaily = tracker.LastDailyReminder("workspace1", "U001")
	if lastDaily.IsZero() {
		t.Error("expected non-zero time after update")
	}

	// Verify it's recent
	if time.Since(lastDaily) > time.Second {
		t.Errorf("expected recent timestamp, got %v", lastDaily)
	}
}

func TestNotificationTracker_ChannelNotifications(t *testing.T) {
	tracker := &NotificationTracker{
		lastDM:                  make(map[string]time.Time),
		lastDaily:               make(map[string]time.Time),
		lastChannelNotification: make(map[string]time.Time),
		lastUserPRChannelTag:    make(map[string]TagInfo),
	}

	// Test initial state
	lastChannel := tracker.LastChannelNotification("workspace1", "owner", "repo", 123)
	if !lastChannel.IsZero() {
		t.Errorf("expected zero time for untracked PR, got %v", lastChannel)
	}

	// Update channel notification
	tracker.UpdateChannelNotification("workspace1", "owner", "repo", 123)

	// Verify it was recorded
	lastChannel = tracker.LastChannelNotification("workspace1", "owner", "repo", 123)
	if lastChannel.IsZero() {
		t.Error("expected non-zero time after update")
	}

	// Verify different PR is separate
	lastChannel2 := tracker.LastChannelNotification("workspace1", "owner", "repo", 124)
	if !lastChannel2.IsZero() {
		t.Error("expected zero time for different PR")
	}

	// Verify different repo is separate
	lastChannel3 := tracker.LastChannelNotification("workspace1", "owner", "other", 123)
	if !lastChannel3.IsZero() {
		t.Error("expected zero time for different repo")
	}
}

func TestNotificationTracker_UserPRChannelTag(t *testing.T) {
	tracker := &NotificationTracker{
		lastDM:                  make(map[string]time.Time),
		lastDaily:               make(map[string]time.Time),
		lastChannelNotification: make(map[string]time.Time),
		lastUserPRChannelTag:    make(map[string]TagInfo),
	}

	// Test initial state - no tag recorded
	tagInfo := tracker.LastUserPRChannelTag("workspace1", "U001", "owner", "repo", 123)
	if !tagInfo.Timestamp.IsZero() {
		t.Errorf("expected zero timestamp for untracked tag, got %v", tagInfo.Timestamp)
	}
	if tagInfo.ChannelID != "" {
		t.Errorf("expected empty channel for untracked tag, got %q", tagInfo.ChannelID)
	}

	// Update tag - user tagged in C123
	tracker.UpdateUserPRChannelTag("workspace1", "U001", "C123", "owner", "repo", 123)

	// Verify tag was recorded
	tagInfo = tracker.LastUserPRChannelTag("workspace1", "U001", "owner", "repo", 123)
	if tagInfo.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp after tag")
	}
	if tagInfo.ChannelID != "C123" {
		t.Errorf("expected channel C123, got %q", tagInfo.ChannelID)
	}

	// CRITICAL TEST: First tag wins - update with different channel should NOT change
	time.Sleep(10 * time.Millisecond) // Ensure different timestamp
	tracker.UpdateUserPRChannelTag("workspace1", "U001", "C456", "owner", "repo", 123)

	// Verify tag still points to first channel (C123)
	tagInfo = tracker.LastUserPRChannelTag("workspace1", "U001", "owner", "repo", 123)
	if tagInfo.ChannelID != "C123" {
		t.Errorf("expected first channel C123 to be preserved, got %q", tagInfo.ChannelID)
	}

	// Verify different PR gets separate tag
	tracker.UpdateUserPRChannelTag("workspace1", "U001", "C789", "owner", "repo", 124)
	tagInfo2 := tracker.LastUserPRChannelTag("workspace1", "U001", "owner", "repo", 124)
	if tagInfo2.ChannelID != "C789" {
		t.Errorf("expected channel C789 for different PR, got %q", tagInfo2.ChannelID)
	}

	// Verify different user gets separate tag
	tracker.UpdateUserPRChannelTag("workspace1", "U002", "C999", "owner", "repo", 123)
	tagInfo3 := tracker.LastUserPRChannelTag("workspace1", "U002", "owner", "repo", 123)
	if tagInfo3.ChannelID != "C999" {
		t.Errorf("expected channel C999 for different user, got %q", tagInfo3.ChannelID)
	}
}

func TestNotificationTracker_Cleanup(t *testing.T) {
	tracker := &NotificationTracker{
		lastDM:                  make(map[string]time.Time),
		lastDaily:               make(map[string]time.Time),
		lastChannelNotification: make(map[string]time.Time),
		lastUserPRChannelTag:    make(map[string]TagInfo),
	}

	now := time.Now()

	// Add some old entries (2 hours ago)
	oldTime := now.Add(-2 * time.Hour)
	tracker.lastDM["workspace1:U001"] = oldTime
	tracker.lastDaily["workspace1:U001"] = oldTime
	tracker.lastChannelNotification["workspace1:owner/repo#123"] = oldTime
	tracker.lastUserPRChannelTag["workspace1:U001:owner/repo#123"] = TagInfo{
		Timestamp: oldTime,
		ChannelID: "C123",
	}

	// Add some recent entries (30 minutes ago)
	recentTime := now.Add(-30 * time.Minute)
	tracker.lastDM["workspace1:U002"] = recentTime
	tracker.lastDaily["workspace1:U002"] = recentTime
	tracker.lastChannelNotification["workspace1:owner/repo#124"] = recentTime
	tracker.lastUserPRChannelTag["workspace1:U002:owner/repo#124"] = TagInfo{
		Timestamp: recentTime,
		ChannelID: "C456",
	}

	// Cleanup entries older than 1 hour
	tracker.Cleanup(1 * time.Hour)

	// Verify old entries were removed
	if _, exists := tracker.lastDM["workspace1:U001"]; exists {
		t.Error("expected old DM entry to be removed")
	}
	if _, exists := tracker.lastDaily["workspace1:U001"]; exists {
		t.Error("expected old daily entry to be removed")
	}
	if _, exists := tracker.lastChannelNotification["workspace1:owner/repo#123"]; exists {
		t.Error("expected old channel notification to be removed")
	}
	if _, exists := tracker.lastUserPRChannelTag["workspace1:U001:owner/repo#123"]; exists {
		t.Error("expected old user PR tag to be removed")
	}

	// Verify recent entries were kept
	if _, exists := tracker.lastDM["workspace1:U002"]; !exists {
		t.Error("expected recent DM entry to be kept")
	}
	if _, exists := tracker.lastDaily["workspace1:U002"]; !exists {
		t.Error("expected recent daily entry to be kept")
	}
	if _, exists := tracker.lastChannelNotification["workspace1:owner/repo#124"]; !exists {
		t.Error("expected recent channel notification to be kept")
	}
	if _, exists := tracker.lastUserPRChannelTag["workspace1:U002:owner/repo#124"]; !exists {
		t.Error("expected recent user PR tag to be kept")
	}
}

func TestNotificationTracker_ConcurrentAccess(t *testing.T) {
	tracker := &NotificationTracker{
		lastDM:                  make(map[string]time.Time),
		lastDaily:               make(map[string]time.Time),
		lastChannelNotification: make(map[string]time.Time),
		lastUserPRChannelTag:    make(map[string]TagInfo),
	}

	// Launch multiple goroutines that read and write concurrently
	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Write operations
				tracker.UpdateDMNotification("workspace1", "U001")
				tracker.UpdateDailyReminder("workspace1", "U001")
				tracker.UpdateChannelNotification("workspace1", "owner", "repo", 123)
				tracker.UpdateUserPRChannelTag("workspace1", "U001", "C123", "owner", "repo", 123)

				// Read operations
				_ = tracker.LastDMNotification("workspace1", "U001")
				_ = tracker.LastDailyReminder("workspace1", "U001")
				_ = tracker.LastChannelNotification("workspace1", "owner", "repo", 123)
				_ = tracker.LastUserPRChannelTag("workspace1", "U001", "owner", "repo", 123)

				// Cleanup operations
				if j%10 == 0 {
					tracker.Cleanup(1 * time.Hour)
				}
			}
		}(i)
	}

	// Wait for all goroutines to complete
	// If there's a race condition, the test will fail with -race flag
	wg.Wait()

	// Verify tracker still works after concurrent access
	tagInfo := tracker.LastUserPRChannelTag("workspace1", "U001", "owner", "repo", 123)
	if tagInfo.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp after concurrent access")
	}
	if tagInfo.ChannelID != "C123" {
		t.Errorf("expected channel C123, got %q", tagInfo.ChannelID)
	}
}

func TestNotificationTracker_KeyFormats(t *testing.T) {
	// Verify that key formats match across update and lookup
	tracker := &NotificationTracker{
		lastDM:                  make(map[string]time.Time),
		lastDaily:               make(map[string]time.Time),
		lastChannelNotification: make(map[string]time.Time),
		lastUserPRChannelTag:    make(map[string]TagInfo),
	}

	// Test that various special characters in keys work correctly
	tests := []struct {
		name      string
		workspace string
		user      string
		owner     string
		repo      string
		prNumber  int
		channelID string
	}{
		{
			name:      "simple keys",
			workspace: "ws1",
			user:      "U001",
			owner:     "owner",
			repo:      "repo",
			prNumber:  123,
			channelID: "C001",
		},
		{
			name:      "keys with special characters",
			workspace: "ws-prod",
			user:      "U_ABC_123",
			owner:     "org-name",
			repo:      "repo.name",
			prNumber:  999,
			channelID: "C_XYZ_789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test DM notification
			tracker.UpdateDMNotification(tt.workspace, tt.user)
			dmTime := tracker.LastDMNotification(tt.workspace, tt.user)
			if dmTime.IsZero() {
				t.Error("DM notification not found after update")
			}

			// Test channel notification
			tracker.UpdateChannelNotification(tt.workspace, tt.owner, tt.repo, tt.prNumber)
			channelTime := tracker.LastChannelNotification(tt.workspace, tt.owner, tt.repo, tt.prNumber)
			if channelTime.IsZero() {
				t.Error("Channel notification not found after update")
			}

			// Test user PR channel tag
			tracker.UpdateUserPRChannelTag(tt.workspace, tt.user, tt.channelID, tt.owner, tt.repo, tt.prNumber)
			tagInfo := tracker.LastUserPRChannelTag(tt.workspace, tt.user, tt.owner, tt.repo, tt.prNumber)
			if tagInfo.ChannelID != tt.channelID {
				t.Errorf("expected channel %q, got %q", tt.channelID, tagInfo.ChannelID)
			}
		})
	}
}
