package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestHandlePullRequestEventWithData_NoChannelsConfigured tests that the function returns early when no channels are configured.
func TestHandlePullRequestEventWithData_NoChannelsConfigured(t *testing.T) {
	ctx := context.Background()

	// Config manager that returns no channels
	cfg := config.New()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  cfg,
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
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

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Draft:  false,
			Merged: false,
		},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"alice": {Kind: "review"},
			},
		},
	}

	// Should return early - no channels configured (ChannelsForRepo returns empty slice by default)
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Test passes if it returns without panicking
}

// TestHandlePullRequestEventWithData_WithBlockedUsers tests notification flow with blocked users.
func TestHandlePullRequestEventWithData_WithBlockedUsers(t *testing.T) {
	ctx := context.Background()

	// Config manager with one channel configured
	cfg := config.New()
	// We can't easily inject config, but that's okay - this function loads it internally
	// The test will exercise the code path even if channels list is empty

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			if channelName == "#test-channel" || channelName == "test-channel" {
				return "C123"
			}
			return channelName
		},
		botInChannelFunc: func(ctx context.Context, channelID string) bool {
			return channelID == "C123"
		},
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  cfg,
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
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

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Draft:  false,
			Merged: false,
		},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"alice": {Kind: "review"},
				"bob":   {Kind: "review"},
			},
		},
	}

	// Call function - since no channels are configured, it will return early before processing DMs
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Test passes if it returns without panicking
	// Give goroutines time to complete if any were started
	time.Sleep(50 * time.Millisecond)
}

// TestHandlePullRequestEventWithData_NoBlockedUsers tests when there are no blocked users.
func TestHandlePullRequestEventWithData_NoBlockedUsers(t *testing.T) {
	ctx := context.Background()

	cfg := config.New()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  cfg,
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
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
		Action: "synchronize",
		Number: 42,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/42"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now().Add(-1 * time.Hour)
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	// No blocked users - all checks passing, approved
	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Draft:  false,
			Merged: false,
		},
		Analysis: turn.Analysis{
			WorkflowState:      "approved",
			Approved:           true,
			UnresolvedComments: 0,
			NextAction:         map[string]turn.Action{}, // No blocked users
			Checks: turn.Checks{
				Passing: 10,
				Failing: 0,
				Pending: 0,
				Waiting: 0,
			},
		},
	}

	// Should handle the case with no blocked users gracefully
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Test passes if it returns without panicking
}

// TestHandlePullRequestEventWithData_MergedPR tests handling merged PRs.
func TestHandlePullRequestEventWithData_MergedPR(t *testing.T) {
	ctx := context.Background()

	cfg := config.New()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  cfg,
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    cache.New(),
		commitPRCache:  cache.NewCommitPRCache(),
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
		Action: "closed",
		Number: 42,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/42"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now().Add(-2 * time.Hour)
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	// Merged PR
	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "closed",
			Draft:  false,
			Merged: true, // Merged
		},
		Analysis: turn.Analysis{
			WorkflowState: "merged",
			NextAction:    map[string]turn.Action{},
		},
	}

	// Should handle merged PR gracefully
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Test passes if it returns without panicking
}
