package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// Test helper to create a basic CheckResponse for testing
func newCheckResponse(workflowState string) *turn.CheckResponse {
	return &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: workflowState,
			NextAction: map[string]turn.Action{
				"testuser": {Kind: "review"},
			},
		},
	}
}

// TestSendPRNotification_StateUnchanged verifies idempotency when PR state hasn't changed.
func TestSendPRNotification_StateUnchanged(t *testing.T) {
	store := &mockStateStore{
		dmMessages: map[string]state.DMInfo{
			"U123:https://github.com/owner/repo/pull/1": {
				LastState: "awaiting_review",
			},
		},
	}

	c := &Coordinator{
		stateStore: store,
		slack:      &mockSlackClient{},
	}

	checkResult := newCheckResponse("awaiting_review")

	err := c.sendPRNotification(context.Background(), dmNotificationRequest{
		UserID:      "U123",
		ChannelID:   "C123",
		ChannelName: "general",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
		CheckResult: checkResult,
	})
	if err != nil {
		t.Errorf("Expected no error for unchanged state, got: %v", err)
	}

	// Verify no updates were attempted (UpdateMessage should not be called)
	slack := c.slack.(*mockSlackClient) //nolint:errcheck // Type assertion for test
	if len(slack.updatedMessages) > 0 {
		t.Error("Expected no message updates when state is unchanged")
	}
}

// TestSendPRNotification_UpdateExistingDM verifies updating an existing DM.
func TestSendPRNotification_UpdateExistingDM(t *testing.T) {
	store := &mockStateStore{
		dmMessages: map[string]state.DMInfo{
			"U123:https://github.com/owner/repo/pull/1": {
				ChannelID: "D123",
				MessageTS: "1234567890.123456",
				LastState: "awaiting_review",
			},
		},
	}

	slack := &mockSlackClient{}
	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: &mockConfigManager{domain: "example.com"},
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("approved")

	err := c.sendPRNotification(context.Background(), dmNotificationRequest{
		UserID:      "U123",
		ChannelID:   "",
		ChannelName: "",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
		CheckResult: checkResult,
	})
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify UpdateMessage was called
	if len(slack.updatedMessages) != 1 {
		t.Fatalf("Expected 1 message update, got %d", len(slack.updatedMessages))
	}

	update := slack.updatedMessages[0]
	if update.ChannelID != "D123" {
		t.Errorf("Expected channelID D123, got %s", update.ChannelID)
	}
	if update.Timestamp != "1234567890.123456" {
		t.Errorf("Expected timestamp 1234567890.123456, got %s", update.Timestamp)
	}

	// Verify state was saved
	savedInfo, exists := store.DMMessage(context.Background(), "U123", "https://github.com/owner/repo/pull/1")
	if !exists {
		t.Fatal("Expected DM info to be saved")
	}
	if savedInfo.LastState != "approved" {
		t.Errorf("Expected LastState 'approved', got '%s'", savedInfo.LastState)
	}
}

// TestSendPRNotification_SendNewDMImmediately verifies sending a new DM when delay is disabled.
func TestSendPRNotification_SendNewDMImmediately(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{}
	config := &mockConfigManager{
		dmDelay: 0, // Delay disabled
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("awaiting_review")

	err := c.sendPRNotification(context.Background(), dmNotificationRequest{
		UserID:      "U123",
		ChannelID:   "",
		ChannelName: "",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
		CheckResult: checkResult,
	})
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify SendDirectMessage was called
	if len(slack.sentDirectMessages) != 1 {
		t.Fatalf("Expected 1 DM sent, got %d", len(slack.sentDirectMessages))
	}

	dm := slack.sentDirectMessages[0]
	if dm.UserID != "U123" {
		t.Errorf("Expected userID U123, got %s", dm.UserID)
	}

	// Verify state was saved
	savedInfo, exists := store.DMMessage(context.Background(), "U123", "https://github.com/owner/repo/pull/1")
	if !exists {
		t.Fatal("Expected DM info to be saved")
	}
	if savedInfo.LastState != "awaiting_review" {
		t.Errorf("Expected LastState 'awaiting_review', got '%s'", savedInfo.LastState)
	}
}

// TestSendPRNotification_QueueDelayedDM verifies queueing a DM for delayed delivery.
func TestSendPRNotification_QueueDelayedDM(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return true // User is in channel
		},
	}
	config := &mockConfigManager{
		dmDelay:   30, // 30 minute delay
		workspace: "test-workspace",
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("awaiting_review")

	err := c.sendPRNotification(context.Background(), dmNotificationRequest{
		UserID:      "U123",
		ChannelID:   "C123", // User was tagged in channel
		ChannelName: "general",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
		CheckResult: checkResult,
	})
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify DM was queued
	if len(store.pendingDMs) != 1 {
		t.Fatalf("Expected 1 queued DM, got %d", len(store.pendingDMs))
	}

	queuedDM := store.pendingDMs[0]
	if queuedDM.UserID != "U123" {
		t.Errorf("Expected userID U123, got %s", queuedDM.UserID)
	}
	if queuedDM.PROwner != "owner" {
		t.Errorf("Expected PROwner 'owner', got '%s'", queuedDM.PROwner)
	}

	// Verify SendAfter is in the future
	if !queuedDM.SendAfter.After(time.Now()) {
		t.Error("Expected SendAfter to be in the future")
	}

	// Verify no immediate DM was sent
	if len(slack.sentDirectMessages) > 0 {
		t.Error("Expected no immediate DM when delay is configured")
	}
}

// TestShouldDelayNewDM_DelayDisabled verifies immediate send when delay is 0.
func TestShouldDelayNewDM_DelayDisabled(t *testing.T) {
	config := &mockConfigManager{
		dmDelay: 0,
	}

	c := &Coordinator{
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	shouldQueue, _ := c.shouldDelayNewDM(context.Background(), "U123", "C123", "general", "owner", "repo")

	if shouldQueue {
		t.Error("Expected shouldQueue=false when delay is disabled")
	}
}

// TestShouldDelayNewDM_NoChannel verifies immediate send when no channel info.
func TestShouldDelayNewDM_NoChannel(t *testing.T) {
	config := &mockConfigManager{
		dmDelay: 30,
	}

	c := &Coordinator{
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	shouldQueue, _ := c.shouldDelayNewDM(context.Background(), "U123", "", "", "owner", "repo")

	if shouldQueue {
		t.Error("Expected shouldQueue=false when no channel info")
	}
}

// TestShouldDelayNewDM_UserNotInChannel verifies immediate send when user not in channel.
func TestShouldDelayNewDM_UserNotInChannel(t *testing.T) {
	slack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return false
		},
	}
	config := &mockConfigManager{
		dmDelay: 30,
	}

	c := &Coordinator{
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	shouldQueue, _ := c.shouldDelayNewDM(context.Background(), "U123", "C123", "general", "owner", "repo")

	if shouldQueue {
		t.Error("Expected shouldQueue=false when user not in channel")
	}
}

// TestShouldDelayNewDM_UserInChannel verifies delayed send when user is in channel.
func TestShouldDelayNewDM_UserInChannel(t *testing.T) {
	slack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return true
		},
	}
	config := &mockConfigManager{
		dmDelay: 30,
	}

	c := &Coordinator{
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	beforeCall := time.Now()
	shouldQueue, sendAfter := c.shouldDelayNewDM(context.Background(), "U123", "C123", "general", "owner", "repo")
	afterCall := time.Now()

	if !shouldQueue {
		t.Error("Expected shouldQueue=true when user is in channel")
	}

	// Verify sendAfter is approximately 30 minutes in the future
	expectedDelay := 30 * time.Minute
	minExpected := beforeCall.Add(expectedDelay)
	maxExpected := afterCall.Add(expectedDelay + time.Second) // Allow 1 second tolerance

	if sendAfter.Before(minExpected) || sendAfter.After(maxExpected) {
		t.Errorf("Expected sendAfter around %v, got %v", minExpected, sendAfter)
	}
}

// TestDerivePRState tests PR state extraction.
func TestDerivePRState(t *testing.T) {
	tests := []struct {
		name        string
		checkResult *turn.CheckResponse
		expected    string
	}{
		{
			name:        "nil checkResult",
			checkResult: nil,
			expected:    "unknown",
		},
		{
			name:        "awaiting_review state",
			checkResult: newCheckResponse("awaiting_review"),
			expected:    "awaiting_review",
		},
		{
			name:        "merged state",
			checkResult: newCheckResponse("merged"),
			expected:    "merged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := derivePRState(tt.checkResult)
			if result != tt.expected {
				t.Errorf("Expected state '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

// TestGetLastState tests the getLastState helper function.
func TestGetLastState(t *testing.T) {
	tests := []struct {
		name     string
		info     state.DMInfo
		exists   bool
		expected string
	}{
		{
			name:     "not exists",
			info:     state.DMInfo{},
			exists:   false,
			expected: "none",
		},
		{
			name: "exists but empty state",
			info: state.DMInfo{
				LastState: "",
			},
			exists:   true,
			expected: "none",
		},
		{
			name: "exists with valid state",
			info: state.DMInfo{
				LastState: "awaiting_review",
			},
			exists:   true,
			expected: "awaiting_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLastState(tt.info, tt.exists)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

// TestGetSentAt tests the getSentAt helper function.
func TestGetSentAt(t *testing.T) {
	fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		info   state.DMInfo
		exists bool
		check  func(t *testing.T, result time.Time)
	}{
		{
			name:   "not exists",
			info:   state.DMInfo{},
			exists: false,
			check: func(t *testing.T, result time.Time) { //nolint:thelper // Test case data, not a helper
				if time.Since(result) > time.Second {
					t.Error("Expected recent time when not exists")
				}
			},
		},
		{
			name: "exists but zero time",
			info: state.DMInfo{
				SentAt: time.Time{},
			},
			exists: true,
			check: func(t *testing.T, result time.Time) { //nolint:thelper // Test case data, not a helper
				if time.Since(result) > time.Second {
					t.Error("Expected recent time when SentAt is zero")
				}
			},
		},
		{
			name: "exists with valid time",
			info: state.DMInfo{
				SentAt: fixedTime,
			},
			exists: true,
			check: func(t *testing.T, result time.Time) { //nolint:thelper // Test case data, not a helper
				if !result.Equal(fixedTime) {
					t.Errorf("Expected %v, got %v", fixedTime, result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getSentAt(tt.info, tt.exists)
			tt.check(t, result)
		})
	}
}

// TestGenerateUUID tests that UUIDs are unique.
func TestGenerateUUID(t *testing.T) {
	uuid1 := generateUUID()
	time.Sleep(time.Millisecond)
	uuid2 := generateUUID()

	if uuid1 == uuid2 {
		t.Error("Expected unique UUIDs, got duplicates")
	}

	if uuid1 == "" || uuid2 == "" {
		t.Error("Expected non-empty UUIDs")
	}
}

// TestUpdateDMMessagesForPR_MergedPR_Simplified tests updating DMs for a merged PR with the simplified system.
func TestUpdateDMMessagesForPR_MergedPR_Simplified(t *testing.T) {
	store := &mockStateStore{
		dmUsers: map[string][]string{
			"https://github.com/owner/repo/pull/1": {"U123", "U456"},
		},
		dmMessages: map[string]state.DMInfo{
			"U123:https://github.com/owner/repo/pull/1": {
				ChannelID: "DU123",
				MessageTS: "1111111111.111111",
				LastState: "awaiting_review",
			},
			"U456:https://github.com/owner/repo/pull/1": {
				ChannelID: "DU456",
				MessageTS: "2222222222.222222",
				LastState: "awaiting_review",
			},
		},
	}

	slack := &mockSlackClient{}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: &mockConfigManager{domain: "example.com"},
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("merged")

	c.updateDMMessagesForPR(context.Background(), prUpdateInfo{
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRState:     "merged",
		PRURL:       "https://github.com/owner/repo/pull/1",
		CheckResult: checkResult,
	})

	// Verify both users' DMs were updated
	if len(slack.updatedMessages) != 2 {
		t.Fatalf("Expected 2 message updates, got %d", len(slack.updatedMessages))
	}

	// Verify states were saved
	for _, userID := range []string{"U123", "U456"} {
		savedInfo, exists := store.DMMessage(context.Background(), userID, "https://github.com/owner/repo/pull/1")
		if !exists {
			t.Errorf("Expected DM info to be saved for user %s", userID)
			continue
		}
		if savedInfo.LastState != "merged" {
			t.Errorf("Expected LastState 'merged' for user %s, got '%s'", userID, savedInfo.LastState)
		}
	}
}

// TestUpdateDMMessagesForPR_NoUsers_Simplified tests that no updates happen when no DM users exist.
func TestUpdateDMMessagesForPR_NoUsers_Simplified(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: &mockConfigManager{domain: "example.com"},
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("merged")

	c.updateDMMessagesForPR(context.Background(), prUpdateInfo{
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRState:     "merged",
		PRURL:       "https://github.com/owner/repo/pull/1",
		CheckResult: checkResult,
	})

	// Verify no updates were attempted
	if len(slack.updatedMessages) > 0 {
		t.Error("Expected no updates when no DM users exist")
	}
}

// TestSendPRNotification_UpdateFailsFallbackToNew tests fallback to new DM when update fails.
func TestSendPRNotification_UpdateFailsFallbackToNew(t *testing.T) {
	store := &mockStateStore{
		dmMessages: map[string]state.DMInfo{
			"U123:https://github.com/owner/repo/pull/1": {
				ChannelID: "D123",
				MessageTS: "1234567890.123456",
				LastState: "awaiting_review",
			},
		},
	}

	slack := &mockSlackClient{
		updateMessageFunc: func(ctx context.Context, channelID, timestamp, text string) error {
			return errors.New("message not found")
		},
	}

	config := &mockConfigManager{
		dmDelay: 0, // No delay
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("approved")

	err := c.sendPRNotification(context.Background(), dmNotificationRequest{
		UserID:      "U123",
		ChannelID:   "",
		ChannelName: "",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
		CheckResult: checkResult,
	})
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Verify both UpdateMessage and SendDirectMessage were called
	if len(slack.updatedMessages) == 0 {
		t.Error("Expected UpdateMessage to be attempted")
	}

	if len(slack.sentDirectMessages) != 1 {
		t.Fatal("Expected SendDirectMessage to be called as fallback")
	}
}

// TestSendDMNotificationsToBlockedUsers tests sending DMs to multiple blocked users.
func TestSendDMNotificationsToBlockedUsers(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{}
	config := &mockConfigManager{
		dmDelay: 0,
		domain:  "example.com",
	}
	userMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U111",
			"user2": "U222",
			"user3": "", // No Slack mapping
		},
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    userMapper,
	}

	uniqueUsers := map[string]bool{
		"user1": true,
		"user2": true,
		"user3": true, // Will fail - no mapping
	}

	event := struct {
		Action      string `json:"action"`
		PullRequest struct {
			HTMLURL   string    `json:"html_url"`
			Title     string    `json:"title"`
			CreatedAt time.Time `json:"created_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
			Number int `json:"number"`
		} `json:"pull_request"`
		Number int `json:"number"`
	}{}
	event.PullRequest.HTMLURL = "https://github.com/owner/repo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	checkResult := newCheckResponse("awaiting_review")

	c.sendDMNotificationsToBlockedUsers(
		context.Background(),
		"test-workspace",
		"owner",
		"repo",
		1,
		uniqueUsers,
		event,
		checkResult,
	)

	// Verify DMs were sent to users with valid mappings
	if len(slack.sentDirectMessages) != 2 {
		t.Fatalf("Expected 2 DMs sent, got %d", len(slack.sentDirectMessages))
	}

	// Check that both users received DMs
	userIDs := make(map[string]bool)
	for _, dm := range slack.sentDirectMessages {
		userIDs[dm.UserID] = true
	}

	if !userIDs["U111"] || !userIDs["U222"] {
		t.Error("Expected DMs sent to U111 and U222")
	}
}
