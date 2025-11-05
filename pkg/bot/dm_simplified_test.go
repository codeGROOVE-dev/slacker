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

// TestSendPRNotification_ConcurrentCallsNoDuplicates tests that concurrent calls don't send duplicate DMs.
func TestSendPRNotification_ConcurrentCallsNoDuplicates(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{}
	config := &mockConfigManager{
		dmDelay: 0, // No delay for simplicity
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("awaiting_review")
	req := dmNotificationRequest{
		CheckResult: checkResult,
		UserID:      "U123",
		ChannelID:   "",
		ChannelName: "",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
	}

	// Call sendPRNotification multiple times concurrently (simulating concurrent webhook events)
	const concurrentCalls = 10
	errChan := make(chan error, concurrentCalls)

	for range concurrentCalls {
		go func() {
			errChan <- c.sendPRNotification(context.Background(), req)
		}()
	}

	// Collect errors
	for range concurrentCalls {
		if err := <-errChan; err != nil {
			t.Errorf("Unexpected error from concurrent call: %v", err)
		}
	}

	// Verify only ONE DM was actually sent (not 10!)
	if len(slack.sentDirectMessages) != 1 {
		t.Errorf("Expected exactly 1 DM sent despite %d concurrent calls, got %d", concurrentCalls, len(slack.sentDirectMessages))
	}
}

// TestSendPRNotification_QueuedThenUpdated tests that queued DMs don't create duplicates on updates.
func TestSendPRNotification_QueuedThenUpdated(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return true // User is in channel, so DM will be queued
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

	// First call: Queue a DM (tests_running state)
	checkResult1 := newCheckResponse("tests_running")
	req1 := dmNotificationRequest{
		CheckResult: checkResult1,
		UserID:      "U123",
		ChannelID:   "C123",
		ChannelName: "general",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
	}

	err := c.sendPRNotification(context.Background(), req1)
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	// Verify DM was queued
	if len(store.pendingDMs) != 1 {
		t.Fatalf("Expected 1 queued DM, got %d", len(store.pendingDMs))
	}

	// Verify no DM sent yet
	if len(slack.sentDirectMessages) != 0 {
		t.Fatalf("Expected no immediate DM, got %d", len(slack.sentDirectMessages))
	}

	// Second call immediately after: PR state changes to awaiting_review
	// This should UPDATE the queued DM, not send immediately!
	checkResult2 := newCheckResponse("awaiting_review")
	req2 := req1 // Copy
	req2.CheckResult = checkResult2
	req2.ChannelID = "" // No channel info this time

	err = c.sendPRNotification(context.Background(), req2)
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}

	// CRITICAL: No immediate DM should be sent - the queued DM should be updated instead
	if len(slack.sentDirectMessages) != 0 {
		t.Errorf("Expected no immediate DM (should update queued DM instead), got %d DMs", len(slack.sentDirectMessages))
	}

	// Verify the queued DM still exists and was updated with new state
	pendingDMs, err := store.PendingDMs(context.Background(), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal("Failed to get pending DMs:", err)
	}
	if len(pendingDMs) != 1 {
		t.Fatalf("Expected 1 queued DM after state change, got %d", len(pendingDMs))
	}
	if pendingDMs[0].PRState != "awaiting_review" {
		t.Errorf("Expected queued DM to have state 'awaiting_review', got '%s'", pendingDMs[0].PRState)
	}
}

// TestUpdateDMMessagesForPR_MultipleConcurrentCalls tests that concurrent update calls don't send duplicates.
func TestUpdateDMMessagesForPR_MultipleConcurrentCalls(t *testing.T) {
	store := &mockStateStore{
		dmUsers: map[string][]string{
			"https://github.com/owner/repo/pull/1": {"U123"},
		},
	}
	slack := &mockSlackClient{}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: &mockConfigManager{domain: "example.com", dmDelay: 0},
		userMapper:    &mockUserMapper{},
	}

	checkResult := newCheckResponse("awaiting_review")
	info := prUpdateInfo{
		CheckResult: checkResult,
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRState:     "awaiting_review",
		PRURL:       "https://github.com/owner/repo/pull/1",
	}

	// Call updateDMMessagesForPR multiple times concurrently (simulating multiple webhook events)
	const concurrentCalls = 6 // This is how many duplicates the user got
	doneChan := make(chan bool, concurrentCalls)

	for range concurrentCalls {
		go func() {
			c.updateDMMessagesForPR(context.Background(), info)
			doneChan <- true
		}()
	}

	// Wait for all to complete
	for range concurrentCalls {
		<-doneChan
	}

	// Verify only ONE DM was actually sent (not 6!)
	if len(slack.sentDirectMessages) != 1 {
		t.Errorf("Expected exactly 1 DM despite %d concurrent calls, got %d DMs", concurrentCalls, len(slack.sentDirectMessages))
	}
}

// TestSendPRNotification_RapidStateChanges tests that rapid legitimate state changes all get processed.
func TestSendPRNotification_RapidStateChanges(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{}
	config := &mockConfigManager{
		dmDelay: 0, // No delay for simplicity
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	// Send 5 notifications with DIFFERENT states in rapid succession (within 30 seconds)
	states := []string{"tests_running", "awaiting_review", "approved", "changes_requested", "awaiting_review"}

	for i, state := range states {
		checkResult := newCheckResponse(state)
		req := dmNotificationRequest{
			CheckResult: checkResult,
			UserID:      "U123",
			ChannelID:   "",
			ChannelName: "",
			Owner:       "owner",
			Repo:        "repo",
			PRNumber:    1,
			PRTitle:     "Test PR",
			PRAuthor:    "author",
			PRURL:       "https://github.com/owner/repo/pull/1",
		}

		err := c.sendPRNotification(context.Background(), req)
		if err != nil {
			t.Errorf("Call %d with state %s failed: %v", i+1, state, err)
		}

		// Verify state was saved
		savedInfo, exists := store.DMMessage(context.Background(), "U123", "https://github.com/owner/repo/pull/1")
		if !exists {
			t.Errorf("After call %d: Expected DM state to be saved", i+1)
		} else if savedInfo.LastState != state {
			t.Errorf("After call %d: Expected LastState '%s', got '%s'", i+1, state, savedInfo.LastState)
		}
	}

	// First call sends DM, next 4 calls update it (or send new if update fails)
	// We should have either 1 DM with 4 updates, or up to 5 DMs if all "updates" became new sends
	// The key is: we should have processed all 5 state changes, not skipped any
	totalOperations := len(slack.sentDirectMessages) + len(slack.updatedMessages)
	if totalOperations < 4 { // At minimum: 1 send + 3 updates (one state appears twice)
		t.Errorf("Expected at least 4 DM operations for 5 state changes (one duplicate), got %d sends + %d updates = %d total",
			len(slack.sentDirectMessages), len(slack.updatedMessages), totalOperations)
	}

	// Verify final state is correct
	savedInfo, _ := store.DMMessage(context.Background(), "U123", "https://github.com/owner/repo/pull/1")
	if savedInfo.LastState != "awaiting_review" {
		t.Errorf("Expected final state 'awaiting_review', got '%s'", savedInfo.LastState)
	}
}

// TestSendPRNotification_QueuedDMCancelledWhenUserNoLongerBlocked tests that a queued DM
// is cancelled if the PR state changes such that the user no longer has actions to take.
func TestSendPRNotification_QueuedDMCancelledWhenUserNoLongerBlocked(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return true // User is in channel, so DM should be queued
		},
	}
	config := &mockConfigManager{
		dmDelay: 30, // 30 minute delay
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	// First call: Queue a DM for the user (user needs to review)
	checkResult1 := newCheckResponse("awaiting_review")
	req1 := dmNotificationRequest{
		CheckResult: checkResult1,
		UserID:      "U123",
		ChannelID:   "C123",
		ChannelName: "general",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
	}

	err := c.sendPRNotification(context.Background(), req1)
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	// Verify DM was queued
	pendingDMs, err := store.PendingDMs(context.Background(), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal("Failed to get pending DMs:", err)
	}
	if len(pendingDMs) != 1 {
		t.Fatalf("Expected 1 queued DM, got %d", len(pendingDMs))
	}

	// Second call: PR state changes such that user no longer has actions (no NextAction)
	// This simulates: user reviewed, PR now needs author to fix tests
	checkResult2 := newCheckResponse("tests_broken")
	checkResult2.Analysis.NextAction = nil // No actions needed (or empty map)
	req2 := req1
	req2.CheckResult = checkResult2

	err = c.sendPRNotification(context.Background(), req2)
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}

	// CRITICAL: The queued DM should be CANCELLED (not sent early, not updated)
	pendingDMs, err = store.PendingDMs(context.Background(), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal("Failed to get pending DMs:", err)
	}
	if len(pendingDMs) != 0 {
		t.Errorf("Expected queued DM to be cancelled (0 pending), got %d pending DMs", len(pendingDMs))
	}

	// No immediate DM should be sent
	if len(slack.sentDirectMessages) != 0 {
		t.Errorf("Expected no immediate DM when cancelling queued DM, got %d DMs", len(slack.sentDirectMessages))
	}
}

// TestSendPRNotification_QueuedDMUpdatedWhenUserLeavesChannel tests that a queued DM
// is properly updated when the user leaves the channel (even though immediate send would now apply).
func TestSendPRNotification_QueuedDMUpdatedWhenUserLeavesChannel(t *testing.T) {
	userInChannel := true // Track whether user is in channel
	store := &mockStateStore{}
	slack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return userInChannel
		},
	}
	config := &mockConfigManager{
		dmDelay: 30, // 30 minute delay
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	// First call: Queue a DM (delay enabled, user in channel)
	checkResult1 := newCheckResponse("awaiting_review")
	req1 := dmNotificationRequest{
		CheckResult: checkResult1,
		UserID:      "U123",
		ChannelID:   "C123",
		ChannelName: "general",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
	}

	err := c.sendPRNotification(context.Background(), req1)
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	// Verify DM was queued
	pendingDMs, err := store.PendingDMs(context.Background(), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal("Failed to get pending DMs:", err)
	}
	if len(pendingDMs) != 1 {
		t.Fatalf("Expected 1 queued DM, got %d", len(pendingDMs))
	}

	// User leaves channel (or gets kicked)
	userInChannel = false

	// Second call: State changes, user NOT in channel anymore
	// Even though immediate send would now apply, the queued DM should be updated (not sent early)
	checkResult2 := newCheckResponse("tests_broken")
	req2 := req1
	req2.CheckResult = checkResult2

	err = c.sendPRNotification(context.Background(), req2)
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}

	// CRITICAL: The queued DM should be UPDATED (not sent early, even though user left channel)
	// This ensures we respect the original delay decision
	pendingDMs, err = store.PendingDMs(context.Background(), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal("Failed to get pending DMs:", err)
	}
	if len(pendingDMs) != 1 {
		t.Errorf("Expected 1 queued DM (updated, not sent early), got %d pending DMs", len(pendingDMs))
	}
	if pendingDMs[0].PRState != "tests_broken" {
		t.Errorf("Expected queued DM state 'tests_broken', got '%s'", pendingDMs[0].PRState)
	}

	// No immediate DM should be sent
	if len(slack.sentDirectMessages) != 0 {
		t.Errorf("Expected no immediate DM (should update queued DM), got %d DMs", len(slack.sentDirectMessages))
	}
}

// TestSendPRNotification_PreviouslySentDMUpdatedEvenWhenUserNoLongerBlocked tests that
// a previously sent DM is updated when the PR state changes, even if the user no longer
// has actions to take.
func TestSendPRNotification_PreviouslySentDMUpdatedEvenWhenUserNoLongerBlocked(t *testing.T) {
	store := &mockStateStore{}
	slack := &mockSlackClient{}
	config := &mockConfigManager{
		dmDelay: 0, // No delay - send immediately
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	// First call: Send a DM (user needs to review)
	checkResult1 := newCheckResponse("awaiting_review")
	req1 := dmNotificationRequest{
		CheckResult: checkResult1,
		UserID:      "U123",
		ChannelID:   "",
		ChannelName: "",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
	}

	err := c.sendPRNotification(context.Background(), req1)
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	// Verify DM was sent
	if len(slack.sentDirectMessages) != 1 {
		t.Fatalf("Expected 1 sent DM, got %d", len(slack.sentDirectMessages))
	}

	// Second call: State changes, user no longer has actions (no NextAction)
	// This simulates: user reviewed, PR now needs author to fix tests
	checkResult2 := newCheckResponse("tests_broken")
	checkResult2.Analysis.NextAction = nil // No actions needed
	req2 := req1
	req2.CheckResult = checkResult2

	err = c.sendPRNotification(context.Background(), req2)
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}

	// CRITICAL: The previously sent DM should be UPDATED (not cancelled, not skipped)
	// Even though user no longer has actions, we update to reflect new state
	if len(slack.updatedMessages) != 1 {
		t.Errorf("Expected 1 DM update, got %d updates", len(slack.updatedMessages))
	}

	// Verify state was saved with new state
	savedInfo, exists := store.DMMessage(context.Background(), "U123", "https://github.com/owner/repo/pull/1")
	if !exists {
		t.Error("Expected DM state to be saved")
	} else if savedInfo.LastState != "tests_broken" {
		t.Errorf("Expected LastState 'tests_broken', got '%s'", savedInfo.LastState)
	}
}

// TestSendPRNotification_QueuedDMBehaviorAcrossRestarts tests that queued DMs persist
// across restarts and are properly handled when the bot restarts.
func TestSendPRNotification_QueuedDMBehaviorAcrossRestarts(t *testing.T) {
	// Simulate a queued DM that exists in the datastore (from before restart)
	existingQueuedDM := &state.PendingDM{
		ID:        "dm-123",
		UserID:    "U123",
		PRURL:     "https://github.com/owner/repo/pull/1",
		PROwner:   "owner",
		PRRepo:    "repo",
		PRNumber:  1,
		PRTitle:   "Test PR",
		PRAuthor:  "author",
		PRState:   "awaiting_review",
		SendAfter: time.Now().Add(10 * time.Minute),
		QueuedAt:  time.Now().Add(-20 * time.Minute),
	}

	store := &mockStateStore{
		pendingDMs: []*state.PendingDM{existingQueuedDM},
	}
	slack := &mockSlackClient{}
	config := &mockConfigManager{
		dmDelay: 30,
	}

	c := &Coordinator{
		stateStore:    store,
		slack:         slack,
		configManager: config,
		userMapper:    &mockUserMapper{},
	}

	// After restart, we get a new webhook event for the same PR
	// State has changed from awaiting_review to tests_broken
	checkResult := newCheckResponse("tests_broken")
	req := dmNotificationRequest{
		CheckResult: checkResult,
		UserID:      "U123",
		ChannelID:   "C123",
		ChannelName: "general",
		Owner:       "owner",
		Repo:        "repo",
		PRNumber:    1,
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRURL:       "https://github.com/owner/repo/pull/1",
	}

	err := c.sendPRNotification(context.Background(), req)
	if err != nil {
		t.Fatalf("Call after restart failed: %v", err)
	}

	// CRITICAL: Should find the existing queued DM and update it (not send immediately)
	pendingDMs, err := store.PendingDMs(context.Background(), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal("Failed to get pending DMs:", err)
	}
	if len(pendingDMs) != 1 {
		t.Fatalf("Expected 1 queued DM after restart, got %d", len(pendingDMs))
	}

	// Verify the queued DM has updated state
	if pendingDMs[0].PRState != "tests_broken" {
		t.Errorf("Expected queued DM to have state 'tests_broken', got '%s'", pendingDMs[0].PRState)
	}

	// No immediate DM should be sent
	if len(slack.sentDirectMessages) != 0 {
		t.Errorf("Expected no immediate DM (should update queued DM), got %d DMs", len(slack.sentDirectMessages))
	}
}
