package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestHandlePullRequestEventWithData_NoChannels(t *testing.T) {
	ctx := context.Background()

	// Use real config manager (will have no channels configured)
	configMgr := config.New()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  configMgr,
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
		eventSemaphore: make(chan struct{}, 10),
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
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{},
		},
	}

	// Should return early since no channels configured
	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)
	// Test passes if it returns without panicking
}
