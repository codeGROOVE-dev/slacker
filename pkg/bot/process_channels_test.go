package bot

import (
	"context"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestProcessChannelsInParallel_InvalidEventType(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  config.New(),
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  "invalid_event", // Wrong type - should be struct with pull_request data
		CheckRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	channels := []string{"#test-channel"}

	// Should return nil due to invalid event type
	result := c.processChannelsInParallel(ctx, prCtx, channels, "test-workspace.slack.com")
	if result != nil {
		t.Errorf("expected nil result for invalid event type, got: %v", result)
	}
}

func TestProcessChannelsInParallel_NoValidChannels(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// Return the name unchanged to simulate channel not found
			return channelName
		},
		botInChannelFunc: func(ctx context.Context, channelID string) bool {
			return false
		},
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  config.New(),
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	event := struct {
		Action      string `json:"action"`
		PullRequest struct {
			HTMLURL   string `json:"html_url"`
			Title     string `json:"title"`
			CreatedAt string `json:"created_at"`
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

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  event,
		CheckRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	channels := []string{"#test-channel"}

	// Should return nil because channel cannot be resolved (bot not in channel)
	result := c.processChannelsInParallel(ctx, prCtx, channels, "test-workspace.slack.com")
	if result != nil {
		t.Errorf("expected nil result when bot not in any channels, got: %v", result)
	}
}

func TestProcessPRForChannel_InvalidEventType(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  config.New(),
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  "invalid_event", // Wrong type
		CheckRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	// Should return nil due to invalid event type
	result := c.processPRForChannel(ctx, prCtx, "#test-channel", "test-workspace.slack.com")
	if result != nil {
		t.Errorf("expected nil result for invalid event type, got: %v", result)
	}
}

func TestProcessPRForChannel_ChannelResolutionFailed(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// Return the name unchanged to simulate channel not found
			return channelName
		},
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  config.New(),
		threadCache:    &ThreadCache{prThreads: make(map[string]ThreadInfo), creating: make(map[string]bool)},
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	event := struct {
		Action      string `json:"action"`
		PullRequest struct {
			HTMLURL   string `json:"html_url"`
			Title     string `json:"title"`
			CreatedAt string `json:"created_at"`
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

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  event,
		CheckRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	// Should return nil because channel cannot be resolved
	result := c.processPRForChannel(ctx, prCtx, "#test-channel", "test-workspace.slack.com")
	if result != nil {
		t.Errorf("expected nil result when channel cannot be resolved, got: %v", result)
	}
}
