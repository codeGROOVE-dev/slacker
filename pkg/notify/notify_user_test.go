package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
)

// mockSlackClient implements SlackClient for testing NotifyUser.
type mockSlackClient struct {
	isUserActiveFunc       func(ctx context.Context, userID string) bool
	isUserInChannelFunc    func(ctx context.Context, channelID, userID string) bool
	userTimezoneFunc       func(ctx context.Context, userID string) (string, error)
	sendDirectMessageFunc  func(ctx context.Context, userID, text string) (dmChannelID, messageTS string, err error)
	hasRecentDMAboutPRFunc func(ctx context.Context, userID, prURL string) (bool, error)
	saveDMMessageInfoFunc  func(ctx context.Context, userID, prURL, dmChannelID, messageTS, message string) error
	apiFunc                func() *slackapi.Client
}

func (m *mockSlackClient) IsUserActive(ctx context.Context, userID string) bool {
	if m.isUserActiveFunc != nil {
		return m.isUserActiveFunc(ctx, userID)
	}
	return true // default: user is active
}

func (m *mockSlackClient) IsUserInChannel(ctx context.Context, channelID, userID string) bool {
	if m.isUserInChannelFunc != nil {
		return m.isUserInChannelFunc(ctx, channelID, userID)
	}
	return false // default: user not in channel
}

func (m *mockSlackClient) UserTimezone(ctx context.Context, userID string) (string, error) {
	if m.userTimezoneFunc != nil {
		return m.userTimezoneFunc(ctx, userID)
	}
	return "America/New_York", nil
}

func (m *mockSlackClient) SendDirectMessage(ctx context.Context, userID, text string) (string, string, error) {
	if m.sendDirectMessageFunc != nil {
		return m.sendDirectMessageFunc(ctx, userID, text)
	}
	return "D123", "1234567890.123456", nil
}

func (m *mockSlackClient) HasRecentDMAboutPR(ctx context.Context, userID, prURL string) (bool, error) {
	if m.hasRecentDMAboutPRFunc != nil {
		return m.hasRecentDMAboutPRFunc(ctx, userID, prURL)
	}
	return false, nil
}

func (m *mockSlackClient) SaveDMMessageInfo(ctx context.Context, userID, prURL, dmChannelID, messageTS, message string) error {
	if m.saveDMMessageInfoFunc != nil {
		return m.saveDMMessageInfoFunc(ctx, userID, prURL, dmChannelID, messageTS, message)
	}
	return nil
}

func (m *mockSlackClient) API() *slackapi.Client {
	if m.apiFunc != nil {
		return m.apiFunc()
	}
	return nil
}

// mockSlackManagerWithClient returns a mock SlackManager that returns a specific client.
type mockSlackManagerWithClient struct {
	client SlackClient
	err    error
}

func (m *mockSlackManagerWithClient) Client(ctx context.Context, teamID string) (SlackClient, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.client, nil
}

// mockConfigManagerCustomizable allows customizing return values for testing.
type mockConfigManagerCustomizable struct {
	dailyRemindersEnabled bool
	reminderDMDelay       int
}

func (m *mockConfigManagerCustomizable) DailyRemindersEnabled(org string) bool {
	return m.dailyRemindersEnabled
}

func (m *mockConfigManagerCustomizable) ReminderDMDelay(org, channel string) int {
	return m.reminderDMDelay
}

// TestNotifyUser_UserInactive tests that notifications are deferred when user is inactive.
func TestNotifyUser_UserInactive(t *testing.T) {
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return false // User is inactive
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	manager := &Manager{
		slackManager: mockSlackMgr,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: &mockConfigManager{},
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:  "test-org",
		Repo:   "test-repo",
		Number: 123,
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)

	// Should not error, but should defer notification
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNotifyUser_AntiSpam tests anti-spam protection (1 minute minimum between DMs).
func TestNotifyUser_AntiSpam(t *testing.T) {
	dmSent := false
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			return "D123", "1234567890.123456", nil
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	manager := &Manager{
		slackManager: mockSlackMgr,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: &mockConfigManager{},
	}

	// Record a recent DM (30 seconds ago)
	manager.Tracker.lastDM["T123:U123"] = time.Now().Add(-30 * time.Second)

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// DM should NOT be sent due to anti-spam protection
	if dmSent {
		t.Error("DM should not be sent due to anti-spam protection (< 1 minute since last DM)")
	}
}

// TestNotifyUser_DelayedDM_UserInChannel tests delayed DM logic when user is in the tagged channel.
func TestNotifyUser_DelayedDM_UserInChannel(t *testing.T) {
	dmSent := false
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return channelID == "C123" // User IS in channel C123
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			return "D123", "1234567890.123456", nil
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	mockConfigMgr := &mockConfigManager{}

	manager := &Manager{
		slackManager: mockSlackMgr,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: mockConfigMgr,
	}

	// User was tagged in channel 30 minutes ago (less than 65 minute delay)
	manager.Tracker.lastUserPRChannelTag["T123:U123:test-org/test-repo#123"] = TagInfo{
		ChannelID: "C123",
		Timestamp: time.Now().Add(-30 * time.Minute),
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// DM should NOT be sent yet (delay not elapsed)
	if dmSent {
		t.Error("DM should not be sent - delay period has not elapsed (30 min < 65 min)")
	}
}

// TestNotifyUser_DelayedDM_UserNotInChannel tests immediate DM when user is NOT in the tagged channel.
func TestNotifyUser_DelayedDM_UserNotInChannel(t *testing.T) {
	dmSent := false
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return false // User is NOT in channel
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			return "D123", "1234567890.123456", nil
		},
		hasRecentDMAboutPRFunc: func(ctx context.Context, userID, prURL string) (bool, error) {
			return false, nil
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	manager := &Manager{
		slackManager: mockSlackMgr,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: &mockConfigManager{},
	}

	// User was tagged in channel C999 (different channel) recently
	manager.Tracker.lastUserPRChannelTag["T123:U123:test-org/test-repo#123"] = TagInfo{
		ChannelID: "C999",
		Timestamp: time.Now().Add(-5 * time.Minute),
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// DM SHOULD be sent immediately (user not in tagged channel)
	if !dmSent {
		t.Error("DM should be sent immediately - user is not in the tagged channel")
	}
}

// TestNotifyUser_DelayElapsed tests that DM is sent after delay period elapses.
func TestNotifyUser_DelayElapsed(t *testing.T) {
	dmSent := false
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return true // User IS in channel
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			return "D123", "1234567890.123456", nil
		},
		hasRecentDMAboutPRFunc: func(ctx context.Context, userID, prURL string) (bool, error) {
			return false, nil
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	mockConfigMgr := &mockConfigManager{}

	manager := &Manager{
		slackManager: mockSlackMgr,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: mockConfigMgr,
	}

	// User was tagged 70 minutes ago (more than 65 minute delay)
	manager.Tracker.lastUserPRChannelTag["T123:U123:test-org/test-repo#123"] = TagInfo{
		ChannelID: "C123",
		Timestamp: time.Now().Add(-70 * time.Minute),
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// DM SHOULD be sent (delay period has elapsed)
	if !dmSent {
		t.Error("DM should be sent - delay period has elapsed (70 min > 65 min)")
	}
}

// TestNotifyUser_RemindersDisabled tests that DM is skipped when reminder_dm_delay is 0.
func TestNotifyUser_RemindersDisabled(t *testing.T) {
	dmSent := false
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			return "D123", "1234567890.123456", nil
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	mockConfigMgr := &mockConfigManagerCustomizable{
		dailyRemindersEnabled: true,
		reminderDMDelay:       0, // Reminders disabled
	}

	manager := &Manager{
		slackManager: mockSlackMgr,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: mockConfigMgr,
	}

	// User was tagged in channel recently
	manager.Tracker.lastUserPRChannelTag["T123:U123:test-org/test-repo#123"] = TagInfo{
		ChannelID: "C123",
		Timestamp: time.Now().Add(-5 * time.Minute),
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// DM should NOT be sent (reminders disabled)
	if dmSent {
		t.Error("DM should not be sent - follow-up reminders are disabled (delay = 0)")
	}
}

// TestNotifyUser_SendDirectMessageError tests error handling when SendDirectMessage fails.
func TestNotifyUser_SendDirectMessageError(t *testing.T) {
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "", "", errors.New("slack API error")
		},
		hasRecentDMAboutPRFunc: func(ctx context.Context, userID, prURL string) (bool, error) {
			return false, nil
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	manager := &Manager{
		slackManager: mockSlackMgr,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: &mockConfigManager{},
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)

	// Should return error from SendDirectMessage
	if err == nil {
		t.Error("expected error from SendDirectMessage failure")
	}
}
