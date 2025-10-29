package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestSendDMNotificationsToSlackUsers_EmptyUserList(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  config.New(),
		notifier:       nil, // Can be nil for empty user list test
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
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
		configManager:  config.New(),
		notifier:       nil, // Can be nil for empty user list test
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
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
		configManager:  config.New(),
		notifier:       nil,
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
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
		configManager:  config.New(),
		notifier:       nil,
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
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
		configManager:  config.New(),
		notifier:       nil,
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
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
		configManager:  config.New(),
		notifier:       nil,
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
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
