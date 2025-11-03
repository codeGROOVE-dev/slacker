package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestProcessPRForChannel_FindOrCreateThreadError tests error handling when findOrCreatePRThread fails.
func TestProcessPRForChannel_FindOrCreateThreadError(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			if channelName == "#test-channel" || channelName == "test-channel" {
				return "C123"
			}
			return channelName
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			// Simulate error in channel history lookup
			return nil, errors.New("slack API error")
		},
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			// Also fail thread creation
			return "", errors.New("failed to post thread")
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
			PullRequest: prx.PullRequest{State: "open"},
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{
					"alice": {Kind: "review"},
				},
			},
		},
	}

	// Should return nil when findOrCreatePRThread fails
	result := c.processPRForChannel(ctx, prCtx, "#test-channel", "test-workspace.slack.com")
	if result != nil {
		t.Errorf("expected nil result when findOrCreatePRThread fails, got: %v", result)
	}
}

// TestProcessPRForChannel_MessageUpdateNeeded tests updating an existing message when content changed.
func TestProcessPRForChannel_MessageUpdateNeeded(t *testing.T) {
	ctx := context.Background()

	updateCalled := false
	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			if channelName == "#test-channel" || channelName == "test-channel" {
				return "C123"
			}
			return channelName
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			// Return existing message with old content
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							Timestamp: "existing.thread",
							Text:      ":hourglass: Old PR title https://github.com/testorg/testrepo/pull/42",
							User:      "B123",
						},
					},
				},
			}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		updateMessageFunc: func(ctx context.Context, channelID, timestamp, text string) error {
			updateCalled = true
			if channelID != "C123" {
				t.Errorf("expected channelID C123, got %s", channelID)
			}
			if timestamp != "existing.thread" {
				t.Errorf("expected timestamp existing.thread, got %s", timestamp)
			}
			return nil
		},
	}

	threadCache := cache.New()
	// Pre-populate cache with old state
	threadCache.Set("testorg/testrepo#42", cache.ThreadInfo{
		ThreadTS:    "existing.thread",
		ChannelID:   "C123",
		LastState:   "tests_broken",
		MessageText: ":hourglass: Old PR title https://github.com/testorg/testrepo/pull/42",
		UpdatedAt:   time.Now().Add(-1 * time.Hour),
	})

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    threadCache,
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
	event.PullRequest.Title = "New PR title"
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now().Add(-2 * time.Hour)

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  event,
		CheckRes: &turn.CheckResponse{
			PullRequest: prx.PullRequest{State: "open"},
			Analysis: turn.Analysis{
				WorkflowState: "awaiting_review",
				NextAction: map[string]turn.Action{
					"alice": {Kind: "review"},
				},
			},
		},
	}

	// Should update message since title changed
	result := c.processPRForChannel(ctx, prCtx, "#test-channel", "test-workspace.slack.com")
	if result == nil {
		t.Error("expected non-nil result")
	}

	if !updateCalled {
		t.Error("expected UpdateMessage to be called")
	}
}

// TestProcessPRForChannel_MessageUpdateError tests error handling when message update fails.
func TestProcessPRForChannel_MessageUpdateError(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			return "C123"
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							Timestamp: "existing.thread",
							Text:      ":hourglass: Old title https://github.com/testorg/testrepo/pull/42",
							User:      "B123",
						},
					},
				},
			}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		updateMessageFunc: func(ctx context.Context, channelID, timestamp, text string) error {
			return errors.New("slack API error")
		},
	}

	threadCache := cache.New()
	threadCache.Set("testorg/testrepo#42", cache.ThreadInfo{
		ThreadTS:    "existing.thread",
		ChannelID:   "C123",
		LastState:   "tests_broken",
		MessageText: ":hourglass: Old title https://github.com/testorg/testrepo/pull/42",
		UpdatedAt:   time.Now().Add(-1 * time.Hour),
	})

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		notifier:       nil,
		userMapper:     &mockUserMapper{},
		threadCache:    threadCache,
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
	event.PullRequest.Title = "New title"
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42
	event.PullRequest.CreatedAt = time.Now().Add(-2 * time.Hour)

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 42,
		State:  "awaiting_review",
		Event:  event,
		CheckRes: &turn.CheckResponse{
			PullRequest: prx.PullRequest{State: "open"},
			Analysis: turn.Analysis{
				WorkflowState: "awaiting_review",
				NextAction:    map[string]turn.Action{},
			},
		},
	}

	// Should handle update error gracefully
	result := c.processPRForChannel(ctx, prCtx, "#test-channel", "test-workspace.slack.com")
	if result == nil {
		t.Error("expected non-nil result even when update fails")
	}
}
