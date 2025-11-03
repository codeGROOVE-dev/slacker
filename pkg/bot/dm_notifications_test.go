package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestSendDMNotificationsToSlackUsers_EmptyUserList(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil, // Can be nil for empty user list test
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
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
	}{
		Action: "opened",
		Number: 42,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/42"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	slackUsers := make(map[string]bool) // Empty map

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{},
		},
	}

	// Should handle empty user list without error
	c.sendDMNotificationsToSlackUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, slackUsers, event, "awaiting_review", checkResult)
	// Test passes if it returns without panicking
}

func TestSendDMNotificationsToGitHubUsers_EmptyUserList(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil, // Can be nil for empty user list test
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
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
	}{
		Action: "opened",
		Number: 42,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/42"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	githubUsers := make(map[string]bool) // Empty map

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{},
		},
	}

	// Should handle empty user list without error
	c.sendDMNotificationsToGitHubUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, githubUsers, event, "awaiting_review", checkResult)
	// Test passes if it returns without panicking
}

func TestUpdateDMMessagesForPR_MergedPRNoDMRecipients(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers:         make(map[string][]string),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	prInfo := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		state:    "merged",
		url:      "https://github.com/testorg/testrepo/pull/42",
		checkRes: nil,
	}

	// Should return early - no DM recipients found
	c.updateDMMessagesForPR(ctx, prInfo)
	// Test passes if it returns without panicking
}

func TestUpdateDMMessagesForPR_NonTerminalStateNoBlockedUsers(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	prInfo := prUpdateInfo{
		owner:  "testorg",
		repo:   "testrepo",
		number: 42,
		state:  "awaiting_review",
		url:    "https://github.com/testorg/testrepo/pull/42",
		checkRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{}, // No blocked users
			},
		},
	}

	// Should return early - no blocked users
	c.updateDMMessagesForPR(ctx, prInfo)
	// Test passes if it returns without panicking
}

func TestUpdateDMMessagesForPR_NonTerminalStateNilCheckResult(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	prInfo := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		state:    "awaiting_review",
		url:      "https://github.com/testorg/testrepo/pull/42",
		checkRes: nil, // Nil check result
	}

	// Should return early - nil check result
	c.updateDMMessagesForPR(ctx, prInfo)
	// Test passes if it returns without panicking
}

func TestUpdateDMMessagesForPR_ClosedPRNoDMRecipients(t *testing.T) {
	ctx := context.Background()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers:         make(map[string][]string),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	prInfo := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		state:    "closed",
		url:      "https://github.com/testorg/testrepo/pull/42",
		checkRes: nil,
	}

	// Should return early - no DM recipients found for closed PR
	c.updateDMMessagesForPR(ctx, prInfo)
	// Test passes if it returns without panicking
}
// TestUpdateDMMessagesForPR_MergedWithRecipients tests DM updates for merged PR with recipients.
func TestUpdateDMMessagesForPR_MergedWithRecipients(t *testing.T) {
	ctx := context.Background()

	prURL := "https://github.com/testorg/testrepo/pull/42"
	mockSlack := &mockSlackClient{}
	mockState := &mockStateStore{
		dmUsers: map[string][]string{
			prURL: {"U123", "U456"},
		},
	}

	c := &Coordinator{
		slack:         mockSlack,
		stateStore:    mockState,
		configManager: NewMockConfig().Build(),
	}

	prInfo := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		title:    "Test PR",
		author:   "testauthor",
		state:    "merged",
		url:      prURL,
		checkRes: nil,
	}

	c.updateDMMessagesForPR(ctx, prInfo)

	// Verify DMs were updated
	if len(mockSlack.updatedDMMessage) != 2 {
		t.Errorf("expected 2 DM updates, got %d", len(mockSlack.updatedDMMessage))
	}
}

// TestUpdateDMMessagesForPR_ClosedWithRecipients tests DM updates for closed PR.
func TestUpdateDMMessagesForPR_ClosedWithRecipients(t *testing.T) {
	ctx := context.Background()

	prURL := "https://github.com/testorg/testrepo/pull/42"
	mockSlack := &mockSlackClient{}
	mockState := &mockStateStore{
		dmUsers: map[string][]string{
			prURL: {"U789"},
		},
	}

	c := &Coordinator{
		slack:         mockSlack,
		stateStore:    mockState,
		configManager: NewMockConfig().Build(),
	}

	prInfo := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		title:    "Test PR",
		author:   "testauthor",
		state:    "closed",
		url:      prURL,
		checkRes: nil,
	}

	c.updateDMMessagesForPR(ctx, prInfo)

	// Verify DM was updated
	if len(mockSlack.updatedDMMessage) != 1 {
		t.Errorf("expected 1 DM update, got %d", len(mockSlack.updatedDMMessage))
	}

	if len(mockSlack.updatedDMMessage) > 0 {
		dm := mockSlack.updatedDMMessage[0]
		if dm.UserID != "U789" {
			t.Errorf("expected UserID U789, got %s", dm.UserID)
		}
		if dm.PRURL != prURL {
			t.Errorf("expected PRURL %s, got %s", prURL, dm.PRURL)
		}
	}
}

// TestUpdateDMMessagesForPR_WithBlockedUsers tests updates for non-terminal state with blocked users.
func TestUpdateDMMessagesForPR_WithBlockedUsers(t *testing.T) {
	ctx := context.Background()

	prURL := "https://github.com/testorg/testrepo/pull/42"
	mockSlack := &mockSlackClient{}
	mockState := &mockStateStore{}

	c := &Coordinator{
		slack:         mockSlack,
		stateStore:    mockState,
		configManager: NewMockConfig().Build(),
		userMapper:    &mockUserMapper{},
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"alice": {Kind: "review"},
			},
		},
	}

	prInfo := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		title:    "Test PR",
		author:   "testauthor",
		state:    "awaiting_review",
		url:      prURL,
		checkRes: checkResult,
	}

	c.updateDMMessagesForPR(ctx, prInfo)

	// Should update DM for blocked user (alice)
	if len(mockSlack.updatedDMMessage) == 0 {
		t.Error("expected at least one DM update for blocked user")
	}
}

// TestUpdateDMMessagesForPR_SkipsSystemUser tests that _system user is skipped.
func TestUpdateDMMessagesForPR_SkipsSystemUser(t *testing.T) {
	ctx := context.Background()

	prURL := "https://github.com/testorg/testrepo/pull/42"
	mockSlack := &mockSlackClient{}
	mockState := &mockStateStore{}

	c := &Coordinator{
		slack:         mockSlack,
		stateStore:    mockState,
		configManager: NewMockConfig().Build(),
		userMapper:    &mockUserMapper{},
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"_system": {Kind: "review"}, // Should be skipped
				"alice":   {Kind: "review"},
			},
		},
	}

	prInfo := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		title:    "Test PR",
		author:   "testauthor",
		state:    "awaiting_review",
		url:      prURL,
		checkRes: checkResult,
	}

	c.updateDMMessagesForPR(ctx, prInfo)

	// Should only update for alice, not _system
	for _, dm := range mockSlack.updatedDMMessage {
		if dm.UserID == "U_system" {
			t.Error("should not send DM to _system user")
		}
	}
}

// TestSendDMNotificationsToGitHubUsers_HappyPath tests successful GitHub->Slack mapping and DM sending.
func TestSendDMNotificationsToGitHubUsers_HappyPath(t *testing.T) {
	ctx := context.Background()

	mockUserMapper := NewMockUserMapper().
		WithMappings(map[string]string{
			"user1": "U001",
			"user2": "U002",
		}).
		Build()

	mockConfig := NewMockConfig().
		WithDomain("test.com").
		Build()

	c := NewTestCoordinator().
		WithUserMapper(mockUserMapper).
		WithConfig(mockConfig).
		Build()
	// Note: we can't easily test notifier.NotifyUser without full integration setup
	// but we can verify the function completes without errors

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
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 42

	uniqueUsers := map[string]bool{
		"user1": true,
		"user2": true,
	}

	checkResult := &turn.CheckResponse{}

	// Should complete without panic
	c.sendDMNotificationsToGitHubUsers(ctx, "test-workspace", "testorg", "testrepo", 42, uniqueUsers, event, "awaiting_review", checkResult)
}

// TestSendDMNotificationsToGitHubUsers_MappingFailure tests when user mapping fails.
func TestSendDMNotificationsToGitHubUsers_MappingFailure(t *testing.T) {
	ctx := context.Background()

	mockUserMapper := NewMockUserMapper().
		WithMappings(map[string]string{
			"user1": "U001",
			// user2 will fail to map
		}).
		Build()

	mockConfig := NewMockConfig().
		WithDomain("test.com").
		Build()

	c := NewTestCoordinator().
		WithUserMapper(mockUserMapper).
		WithConfig(mockConfig).
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
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 42

	uniqueUsers := map[string]bool{
		"user1": true,
		"user2": true, // This will fail to map
	}

	checkResult := &turn.CheckResponse{}

	// Should handle mapping failures gracefully
	c.sendDMNotificationsToGitHubUsers(ctx, "test-workspace", "testorg", "testrepo", 42, uniqueUsers, event, "awaiting_review", checkResult)
}

// TestSendDMNotificationsToSlackUsers_HappyPath tests delayed DM notification logic.
func TestSendDMNotificationsToSlackUsers_HappyPath(t *testing.T) {
	ctx := context.Background()

	mockConfig := NewMockConfig().
		WithDomain("test.com").
		Build()

	c := NewTestCoordinator().
		WithConfig(mockConfig).
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
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 42

	uniqueSlackUsers := map[string]bool{
		"U001": true,
		"U002": true,
	}

	checkResult := &turn.CheckResponse{}

	// Should process users and set up delayed DMs
	c.sendDMNotificationsToSlackUsers(ctx, "test-workspace", "testorg", "testrepo", 42, uniqueSlackUsers, event, "awaiting_review", checkResult)
}

// TestSendDMNotificationsToSlackUsers_ImmediateDM tests immediate DM when not tagged in channel.
func TestSendDMNotificationsToSlackUsers_ImmediateDM(t *testing.T) {
	ctx := context.Background()

	mockConfig := NewMockConfig().
		WithDomain("test.com").
		Build()

	c := NewTestCoordinator().
		WithConfig(mockConfig).
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
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 42

	uniqueSlackUsers := map[string]bool{
		"U001": true,
	}

	checkResult := &turn.CheckResponse{}

	// Should send immediate DM when user wasn't tagged in channels
	c.sendDMNotificationsToSlackUsers(ctx, "test-workspace", "testorg", "testrepo", 42, uniqueSlackUsers, event, "awaiting_review", checkResult)
}
