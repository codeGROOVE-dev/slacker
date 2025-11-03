package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestSendDMNotificationsToSlackUsers_NilNotifier tests that nil notifier is handled gracefully.
func TestSendDMNotificationsToSlackUsers_NilNotifier(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    &mockStateStore{processedEvents: make(map[string]bool)},
		configManager: config.New(),
		notifier:      nil, // Test with nil notifier
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
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
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now()

	slackUsers := map[string]bool{
		"U001": true,
		"U002": true,
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"alice": {Kind: "review"},
			},
		},
	}

	// Call function - should complete without panicking despite nil notifier
	c.sendDMNotificationsToSlackUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, slackUsers, event, "awaiting_review", checkResult)

	// Test passes if it completes without panicking
}

// TestSendDMNotificationsToSlackUsers_NoCheckResult tests when checkResult is nil.
func TestSendDMNotificationsToSlackUsers_NoCheckResult(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    &mockStateStore{processedEvents: make(map[string]bool)},
		configManager: config.New(),
		notifier:      nil,
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
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
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now()

	slackUsers := map[string]bool{
		"U001": true,
	}

	// Call function - should complete without panicking
	c.sendDMNotificationsToSlackUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, slackUsers, event, "awaiting_review", nil)

	// Test passes if it completes without panicking
}

// TestSendDMNotificationsToGitHubUsers_MappingSuccess tests GitHub->Slack mapping path.
func TestSendDMNotificationsToGitHubUsers_MappingSuccess(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"alice": "U001",
			"bob":   "U002",
		},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    &mockStateStore{processedEvents: make(map[string]bool)},
		configManager: config.New(),
		notifier:      nil,
		userMapper:    mockMapper,
		threadCache:   cache.New(),
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
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now()

	githubUsers := map[string]bool{
		"alice": true,
		"bob":   true,
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"alice": {Kind: "review"},
				"bob":   {Kind: "review"},
			},
		},
	}

	// Call function - should complete without panicking
	c.sendDMNotificationsToGitHubUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, githubUsers, event, "awaiting_review", checkResult)

	// Test passes if it completes without panicking
}

// TestSendDMNotificationsToGitHubUsers_MappingFailures tests handling when GitHub->Slack mapping fails.
func TestSendDMNotificationsToGitHubUsers_MappingFailures(t *testing.T) {
	ctx := context.Background()

	// Mock mapper that fails all lookups
	mockMapper := &mockUserMapper{
		failLookups: true,
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    &mockStateStore{processedEvents: make(map[string]bool)},
		configManager: config.New(),
		notifier:      nil,
		userMapper:    mockMapper,
		threadCache:   cache.New(),
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
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now()

	githubUsers := map[string]bool{
		"alice": true,
		"bob":   true,
	}

	// Call function - should complete without panicking even when mapping fails
	c.sendDMNotificationsToGitHubUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, githubUsers, event, "awaiting_review", nil)

	// Test passes if it completes without panicking
}

// TestSendDMNotificationsToGitHubUsers_PartialMappingFailures tests when only some users map successfully.
func TestSendDMNotificationsToGitHubUsers_PartialMappingFailures(t *testing.T) {
	ctx := context.Background()

	// Mock mapper that only maps alice, not bob
	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"alice": "U001",
			// bob not in mapping - will fail
		},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    &mockStateStore{processedEvents: make(map[string]bool)},
		configManager: config.New(),
		notifier:      nil,
		userMapper:    mockMapper,
		threadCache:   cache.New(),
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
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now()

	githubUsers := map[string]bool{
		"alice": true,
		"bob":   true, // Will fail mapping
	}

	// Call function - should complete without panicking
	c.sendDMNotificationsToGitHubUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, githubUsers, event, "awaiting_review", nil)

	// Test passes if it completes without panicking
}

// TestSendDMNotificationsToGitHubUsers_NilCheckResult tests nil checkResult handling.
func TestSendDMNotificationsToGitHubUsers_NilCheckResult(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"alice": "U001",
		},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    &mockStateStore{processedEvents: make(map[string]bool)},
		configManager: config.New(),
		notifier:      nil,
		userMapper:    mockMapper,
		threadCache:   cache.New(),
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
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now()

	githubUsers := map[string]bool{
		"alice": true,
	}

	// Call function - should complete without panicking
	c.sendDMNotificationsToGitHubUsers(ctx, "test-workspace.slack.com", "testorg", "testrepo", 42, githubUsers, event, "awaiting_review", nil)

	// Test passes if it completes without panicking
}
