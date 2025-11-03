package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

func TestProcessChannelsInParallel_InvalidEventType(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
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
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	event := struct {
		Action      string `json:"action"`
		PullRequest struct {
			HTMLURL   string `json:"html_url"`
			Title     string `json:"title"`
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
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
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
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	event := struct {
		Action      string `json:"action"`
		PullRequest struct {
			HTMLURL   string `json:"html_url"`
			Title     string `json:"title"`
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

// TestProcessChannelsInParallel_HappyPath tests successful parallel channel processing.
func TestProcessChannelsInParallel_HappyPath(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// Simulate successful channel resolution
			switch channelName {
			case "#channel1", "channel1":
				return "C111"
			case "#channel2", "channel2":
				return "C222"
			default:
				return channelName
			}
		},
		botInChannelFunc: func(ctx context.Context, channelID string) bool {
			// Bot is in both channels
			return channelID == "C111" || channelID == "C222"
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			// No existing messages
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{},
			}, nil
		},
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	event := struct {
		Action      string `json:"action"`
		PullRequest struct {
			HTMLURL   string `json:"html_url"`
			Title     string `json:"title"`
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

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  event,
		CheckRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{
					"alice": {Kind: "review"},
				},
			},
		},
	}

	channels := []string{"#channel1", "#channel2"}

	// Should process both channels in parallel and return tagged users
	result := c.processChannelsInParallel(ctx, prCtx, channels, "test-workspace.slack.com")

	// Result should not be nil (channels were processed)
	if result == nil {
		t.Error("expected non-nil result for successful channel processing")
	}
}

// TestProcessChannelsInParallel_SomeChannelsInvalid tests when only some channels are valid.
func TestProcessChannelsInParallel_SomeChannelsInvalid(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// Only channel1 resolves successfully
			if channelName == "#channel1" || channelName == "channel1" {
				return "C111"
			}
			// channel2 doesn't resolve (returns original name)
			return channelName
		},
		botInChannelFunc: func(ctx context.Context, channelID string) bool {
			return channelID == "C111"
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{},
			}, nil
		},
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
		workspaceName:  "test-workspace.slack.com",
	}

	event := struct {
		Action      string `json:"action"`
		PullRequest struct {
			HTMLURL   string `json:"html_url"`
			Title     string `json:"title"`
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

	channels := []string{"#channel1", "#channel2", "#channel3"}

	// Should process only channel1 (other channels are invalid)
	result := c.processChannelsInParallel(ctx, prCtx, channels, "test-workspace.slack.com")

	// Result should not be nil (at least one channel was processed)
	if result == nil {
		t.Error("expected non-nil result when at least one channel is valid")
	}
}

// TestProcessPRForChannel_UserMappingFailures tests when user mapping fails for blocked users.
func TestProcessPRForChannel_UserMappingFailures(t *testing.T) {
	ctx := context.Background()

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
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{},
			}, nil
		},
	}

	// User mapper that fails all lookups
	mockMapper := &mockUserMapper{
		failLookups: true,
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		userMapper:     mockMapper,
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
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now()

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  event,
		CheckRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{
					"alice": {Kind: "review"},
					"bob":   {Kind: "review"},
				},
			},
		},
	}

	// Process PR for channel - should handle user mapping failures gracefully
	result := c.processPRForChannel(ctx, prCtx, "#test-channel", "test-workspace.slack.com")

	// Should still return result even if user mapping failed
	if result == nil {
		t.Error("expected non-nil result even with user mapping failures")
	}

	// Tagged users should be empty since all lookups failed
	if len(result) != 0 {
		t.Errorf("expected empty tagged users map when all lookups fail, got: %v", result)
	}
}
