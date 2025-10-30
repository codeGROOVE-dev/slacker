package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	slackapi "github.com/slack-go/slack"
)

// mockSlackClient implements SlackClient for testing NotifyUser.
type mockSlackClient struct {
	isUserActiveFunc       func(ctx context.Context, userID string) bool
	isUserInChannelFunc    func(ctx context.Context, channelID, userID string) bool
	userTimezoneFunc       func(ctx context.Context, userID string) (string, error)
	sendDirectMessageFunc  func(ctx context.Context, userID, text string) (dmChannelID, messageTS string, err error)
	updateDMMessageFunc    func(ctx context.Context, userID, prURL, newText string) error
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

func (m *mockSlackClient) UpdateDMMessage(ctx context.Context, userID, prURL, newText string) error {
	if m.updateDMMessageFunc != nil {
		return m.updateDMMessageFunc(ctx, userID, prURL, newText)
	}
	// Default: return ErrNoDMToUpdate (no DM exists to update)
	return slack.ErrNoDMToUpdate
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
	mockSt := &mockStore{}

	manager := &Manager{
		slackManager:  mockSlackMgr,
		configManager: mockConfigMgr,
		store:         mockSt,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
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
	mockSt := &mockStore{}

	manager := &Manager{
		slackManager:  mockSlackMgr,
		configManager: mockConfigMgr,
		store:         mockSt,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
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

// mockStoreCustomizable allows customizing store behavior for testing.
type mockStoreCustomizable struct {
	queuePendingDMFunc  func(dm state.PendingDM) error
	getPendingDMsFunc   func(before time.Time) ([]state.PendingDM, error)
	removePendingDMFunc func(id string) error
}

func (m *mockStoreCustomizable) QueuePendingDM(dm state.PendingDM) error {
	if m.queuePendingDMFunc != nil {
		return m.queuePendingDMFunc(dm)
	}
	return nil
}

func (m *mockStoreCustomizable) GetPendingDMs(before time.Time) ([]state.PendingDM, error) {
	if m.getPendingDMsFunc != nil {
		return m.getPendingDMsFunc(before)
	}
	return nil, nil
}

func (m *mockStoreCustomizable) RemovePendingDM(id string) error {
	if m.removePendingDMFunc != nil {
		return m.removePendingDMFunc(id)
	}
	return nil
}

// TestProcessPendingDMs tests the processPendingDMs function.
func TestProcessPendingDMs(t *testing.T) {
	dmsSent := make([]string, 0)

	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true // All users active
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmsSent = append(dmsSent, userID)
			return "D123", "1234567890.123456", nil
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{client: mockClient}
	mockSt := &mockStoreCustomizable{
		getPendingDMsFunc: func(before time.Time) ([]state.PendingDM, error) {
			now := time.Now()
			// Return 2 DMs that are ready to send
			return []state.PendingDM{
				{
					ID:          "dm-001",
					WorkspaceID: "T123",
					UserID:      "U001",
					PROwner:     "test-org",
					PRRepo:      "test-repo",
					PRNumber:    123,
					PRURL:       "https://github.com/test-org/test-repo/pull/123",
					PRTitle:     "Test PR 1",
					SendAfter:   now.Add(-5 * time.Minute), // Ready to send
				},
				{
					ID:          "dm-002",
					WorkspaceID: "T123",
					UserID:      "U002",
					PROwner:     "test-org",
					PRRepo:      "test-repo",
					PRNumber:    456,
					PRURL:       "https://github.com/test-org/test-repo/pull/456",
					PRTitle:     "Test PR 2",
					SendAfter:   now.Add(-10 * time.Minute), // Ready to send
				},
			}, nil
		},
		removePendingDMFunc: func(id string) error {
			return nil // Successfully removed
		},
	}

	manager := &Manager{
		slackManager: mockSlackMgr,
		store:        mockSt,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
		configManager: &mockConfigManager{},
	}

	ctx := context.Background()
	err := manager.processPendingDMs(ctx)
	if err != nil {
		t.Fatalf("unexpected error processing pending DMs: %v", err)
	}

	// Verify both DMs were sent
	if len(dmsSent) != 2 {
		t.Errorf("expected 2 DMs to be sent, got %d", len(dmsSent))
	}

	// Verify correct users received DMs
	userMap := make(map[string]bool)
	for _, userID := range dmsSent {
		userMap[userID] = true
	}
	if !userMap["U001"] {
		t.Error("expected U001 to receive DM")
	}
	if !userMap["U002"] {
		t.Error("expected U002 to receive DM")
	}
}

// TestProcessPendingDMs_EmptyQueue tests processPendingDMs with no pending DMs.
func TestProcessPendingDMs_EmptyQueue(t *testing.T) {
	mockSt := &mockStoreCustomizable{
		getPendingDMsFunc: func(before time.Time) ([]state.PendingDM, error) {
			return []state.PendingDM{}, nil // No pending DMs
		},
	}

	manager := &Manager{
		store: mockSt,
		Tracker: &NotificationTracker{
			lastDM:                  make(map[string]time.Time),
			lastDaily:               make(map[string]time.Time),
			lastChannelNotification: make(map[string]time.Time),
			lastUserPRChannelTag:    make(map[string]TagInfo),
		},
	}

	ctx := context.Background()
	err := manager.processPendingDMs(ctx)
	if err != nil {
		t.Fatalf("unexpected error with empty queue: %v", err)
	}
}

// TestProcessPendingDMs_StoreError tests error handling when store fails.
func TestProcessPendingDMs_StoreError(t *testing.T) {
	mockSt := &mockStoreCustomizable{
		getPendingDMsFunc: func(before time.Time) ([]state.PendingDM, error) {
			return nil, errors.New("database error")
		},
	}

	manager := &Manager{
		store: mockSt,
	}

	ctx := context.Background()
	err := manager.processPendingDMs(ctx)
	if err == nil {
		t.Error("expected error when store fails")
	}
}

// TestSendDMNow tests the sendDMNow function.
func TestSendDMNow(t *testing.T) {
	dmSent := false
	var sentMessage string

	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			sentMessage = text
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

	ctx := context.Background()
	pr := PRInfo{
		Owner:         "test-org",
		Repo:          "test-repo",
		Number:        123,
		HTMLURL:       "https://github.com/test-org/test-repo/pull/123",
		Title:         "Test PR",
		WorkflowState: "awaiting_review",
	}

	err := manager.sendDMNow(ctx, "T123", "U001", "C123", "test-channel", pr)
	if err != nil {
		t.Fatalf("unexpected error sending DM: %v", err)
	}

	if !dmSent {
		t.Error("expected DM to be sent")
	}

	// Verify message contains PR info
	if sentMessage == "" {
		t.Error("expected non-empty message")
	}
}

// TestSendDMNow_UserInactive tests sendDMNow skips inactive users.
func TestSendDMNow_UserInactive(t *testing.T) {
	dmSent := false

	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return false // User inactive
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
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.sendDMNow(ctx, "T123", "U001", "C123", "test-channel", pr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DM should NOT be sent (user inactive)
	if dmSent {
		t.Error("DM should not be sent to inactive user")
	}
}

// TestSendDMNow_AntiSpam tests sendDMNow respects anti-spam limits.
func TestSendDMNow_AntiSpam(t *testing.T) {
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
	}

	// Record a recent DM (30 seconds ago)
	manager.Tracker.lastDM["T123:U001"] = time.Now().Add(-30 * time.Second)

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.sendDMNow(ctx, "T123", "U001", "C123", "test-channel", pr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DM should NOT be sent (anti-spam protection)
	if dmSent {
		t.Error("DM should not be sent due to anti-spam protection (< 1 minute since last DM)")
	}
}

// TestSendDMNow_SlackError tests error handling when Slack API fails.
func TestSendDMNow_SlackError(t *testing.T) {
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "", "", errors.New("slack API error")
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
	}

	ctx := context.Background()
	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.sendDMNow(ctx, "T123", "U001", "C123", "test-channel", pr)
	if err == nil {
		t.Error("expected error when Slack API fails")
	}
}
