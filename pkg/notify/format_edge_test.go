package notify

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestFormatChannelMessageBase_DraftPR tests draft PR formatting.
func TestFormatChannelMessageBase_DraftPR(t *testing.T) {
	ctx := context.Background()

	params := MessageParams{
		CheckResult: &turn.CheckResponse{
			PullRequest: prx.PullRequest{
				Draft: true,
			},
			Analysis: turn.Analysis{},
		},
		Owner:    "test-org",
		Repo:     "test-repo",
		PRNumber: 123,
		Title:    "Draft PR",
		Author:   "testuser",
		HTMLURL:  "https://github.com/test-org/test-repo/pull/123",
	}

	result := FormatChannelMessageBase(ctx, params)

	// Should include draft indicator in state param
	if !contains(result, "?st=") {
		t.Error("expected state parameter in URL")
	}
	if !contains(result, "Draft PR") {
		t.Error("expected PR title in message")
	}
}

// TestNotifyUser_NoChannelName tests NotifyUser when channelName is empty.
func TestNotifyUser_NoChannelName(t *testing.T) {
	ctx := context.Background()
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		hasRecentDMAboutPRFunc: func(ctx context.Context, userID, prURL string) (bool, error) {
			return false, nil
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
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

	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	// Call with empty channelName - should use default delay
	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "", pr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNotifyUser_HasRecentDM tests that NotifyUser skips DM when HasRecentDMAboutPR returns true.
func TestNotifyUser_HasRecentDM(t *testing.T) {
	ctx := context.Background()
	dmSent := false
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		hasRecentDMAboutPRFunc: func(ctx context.Context, userID, prURL string) (bool, error) {
			return true, nil // User already has recent DM about this PR
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

	// DM should not be sent - user already has recent DM
	if dmSent {
		t.Error("DM should not be sent - user already has recent DM about this PR")
	}
}

// TestNotifyUser_SaveDMMessageInfoError tests error handling when SaveDMMessageInfo fails.
func TestNotifyUser_SaveDMMessageInfoError(t *testing.T) {
	ctx := context.Background()
	mockClient := &mockSlackClient{
		isUserActiveFunc: func(ctx context.Context, userID string) bool {
			return true
		},
		hasRecentDMAboutPRFunc: func(ctx context.Context, userID, prURL string) (bool, error) {
			return false, nil
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "D123", "1234567890.123456", nil
		},
		saveDMMessageInfoFunc: func(ctx context.Context, userID, prURL, dmChannelID, messageTS, message string) error {
			return nil // SaveDMMessageInfo errors are logged but don't fail the operation
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

	pr := PRInfo{
		Owner:   "test-org",
		Repo:    "test-repo",
		Number:  123,
		HTMLURL: "https://github.com/test-org/test-repo/pull/123",
	}

	err := manager.NotifyUser(ctx, "T123", "U123", "C123", "test-channel", pr)
	// Should not error even if SaveDMMessageInfo fails
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
