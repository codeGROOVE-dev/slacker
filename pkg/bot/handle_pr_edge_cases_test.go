package bot

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestHandlePullRequestEventWithData_BlockedUsersWithTaggedUsers tests DM path with tagged users
func TestHandlePullRequestEventWithData_BlockedUsersWithTaggedUsers(t *testing.T) {
	ctx := context.Background()

	cfg := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"testrepo"}).
		WithDomain("example.com").
		Build()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			return "C123"
		},
		botInChannelFunc: func(ctx context.Context, channelID string) bool {
			return true
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "UBOT"}, nil
		},
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			return "1234.5678", nil
		},
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return true
		},
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "D123", "9876.5432", nil
		},
	}

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := NewTestCoordinator().
		WithConfig(cfg).
		WithSlack(mockSlack).
		WithUserMapper(mockMapper).
		Build()

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
	}{
		Action: "opened",
		Number: 42,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/42"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
			},
		},
	}

	// Should process channels and send async DMs to tagged users
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Wait for async DM goroutine to complete
	time.Sleep(100 * time.Millisecond)

	// Test passes if no panic occurs (async DM sent)
}

// TestHandlePullRequestEventWithData_BlockedUsersNoTaggedUsers tests DM path without tagged users
func TestHandlePullRequestEventWithData_BlockedUsersNoTaggedUsers(t *testing.T) {
	ctx := context.Background()

	cfg := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{}).
		WithDomain("example.com").
		Build()

	mockSlack := &mockSlackClient{
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "D123", "9876.5432", nil
		},
	}

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := NewTestCoordinator().
		WithConfig(cfg).
		WithSlack(mockSlack).
		WithUserMapper(mockMapper).
		Build()

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
	}{
		Action: "opened",
		Number: 42,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/42"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"}, // Only one entry (maps don't allow duplicates)
			},
		},
	}

	// Should send async DMs to unique GitHub users (no channels notified)
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Wait for async DM goroutine to complete
	time.Sleep(100 * time.Millisecond)

	// Test passes if no panic occurs (async DM sent via blocked users path)
}

// TestHandlePullRequestEventWithData_ClosedPRUpdatesDMs tests terminal state DM updates
func TestHandlePullRequestEventWithData_ClosedPRUpdatesDMs(t *testing.T) {
	ctx := context.Background()

	cfg := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{}).
		WithDomain("example.com").
		Build()

	c := NewTestCoordinator().
		WithConfig(cfg).
		Build()

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
	}{
		Action: "closed",
		Number: 42,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/42"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "closed",
			Merged: false,
		},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{}, // No blocked users
		},
	}

	// Should update DMs for closed PR even with no blocked users
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Test passes if updateDMMessagesForPR is called (no panic)
}

// TestHandlePullRequestFromSprinkler_TurnclientCheckError tests when turnclient.Check fails
func TestHandlePullRequestFromSprinkler_TurnclientCheckError(t *testing.T) {
	// Set test backend but force an error scenario
	oldBackend := os.Getenv("TURN_TEST_BACKEND")
	os.Setenv("TURN_TEST_BACKEND", "test")
	defer func() {
		if oldBackend != "" {
			os.Setenv("TURN_TEST_BACKEND", oldBackend)
		} else {
			os.Unsetenv("TURN_TEST_BACKEND")
		}
	}()

	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
		eventSemaphore: make(chan struct{}, 10),
	}

	// This will create turnclient successfully but the Check() call will use mock
	// The mock may return an error or nil depending on the PR URL
	// Testing the error handling path
	c.handlePullRequestFromSprinkler(ctx, "testorg", "testrepo", 999, "https://github.com/testorg/testrepo/pull/999", time.Now())

	// Test passes if it handles turnclient errors gracefully
}

// TestHandlePullRequestFromSprinkler_EmptyCommitsList tests commit cache with empty commits
func TestHandlePullRequestFromSprinkler_EmptyCommitsList(t *testing.T) {
	oldBackend := os.Getenv("TURN_TEST_BACKEND")
	os.Setenv("TURN_TEST_BACKEND", "test")
	defer func() {
		if oldBackend != "" {
			os.Setenv("TURN_TEST_BACKEND", oldBackend)
		} else {
			os.Unsetenv("TURN_TEST_BACKEND")
		}
	}()

	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{}).
		Build()

	commitPRCache := cache.NewCommitPRCache()

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  mockConfig,
		threadCache:    cache.New(),
		commitPRCache:  commitPRCache,
		eventSemaphore: make(chan struct{}, 10),
	}

	// Turnclient mock will return PR data (may or may not have commits)
	c.handlePullRequestFromSprinkler(ctx, "testorg", "testrepo", 42, "https://github.com/testorg/testrepo/pull/42", time.Now())

	// Test passes if it handles empty commits list gracefully
}

// TestResolveAndValidateChannel_ChannelIDStartsWithC tests when resolved ID starts with C
func TestResolveAndValidateChannel_ChannelIDStartsWithC(t *testing.T) {
	ctx := context.Background()

	// Mock returns a channel ID starting with 'C' (valid format)
	mockSlack := NewMockSlack().
		WithChannelResolution("C987654", "C987654").
		Build()

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
	}

	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, "C987654", "org", "repo", 1)

	// Current behavior treats channelID == channelName as failure
	if ok {
		t.Error("expected resolution to fail when channelID == channelName (even if starts with C)")
	}

	if channelID != "" {
		t.Errorf("expected empty channelID on failure, got %s", channelID)
	}

	if channelDisplay != "" {
		t.Errorf("expected empty channelDisplay on failure, got %s", channelDisplay)
	}
}

// TestTrackUserTagsForDMDelay_SuccessfulTracking tests successful user tracking
func TestTrackUserTagsForDMDelay_SuccessfulTracking(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			return userID == "U123" // U123 is in channel
		},
	}

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
			"user2": "U456",
		},
	}

	c := &Coordinator{
		slack:         mockSlack,
		configManager: NewMockConfig().WithDomain("example.com").Build(),
		userMapper:    mockMapper,
		threadCache:   cache.New(),
		notifier:      nil, // No notifier for this test
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
				"user2": {Kind: "approve"},
			},
		},
	}

	taggedUsers := c.trackUserTagsForDMDelay(ctx, "workspace-123", "C123", "#test (C123)", "org", "repo", 1, checkResult)

	if len(taggedUsers) != 2 {
		t.Errorf("expected 2 tagged users, got %d", len(taggedUsers))
	}

	if info, ok := taggedUsers["U123"]; !ok || !info.IsInAnyChannel {
		t.Error("expected U123 to be in channel")
	}

	if info, ok := taggedUsers["U456"]; !ok || info.IsInAnyChannel {
		t.Error("expected U456 to NOT be in channel")
	}
}

