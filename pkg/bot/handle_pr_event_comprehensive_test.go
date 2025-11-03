package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestHandlePullRequestEventWithData_Success tests the full happy path with channels and user tagging.
func TestHandlePullRequestEventWithData_Success(t *testing.T) {
	ctx := context.Background()

	var postedMu sync.Mutex
	postedThreads := []string{}

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
		}).
		WithBotInChannel(true).
		WithPostThreadSuccess("1234.567").
		Build()

	mockSlack.postThreadFunc = func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
		postedMu.Lock()
		postedThreads = append(postedThreads, channelID)
		postedMu.Unlock()
		return "1234.567", nil
	}

	mockSlack.botInfoFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{UserID: "UBOT"}, nil
	}

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering"}).
		WithWorkspace("test-workspace.slack.com").
		Build()

	mapper := NewMockUserMapper().WithMappings(map[string]string{
		"alice": "U_ALICE",
		"bob":   "U_BOB",
	}).Build()

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		WithUserMapper(mapper).
		WithWorkspaceName("test-workspace.slack.com").
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
	event.PullRequest.Title = "Add new feature"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "testauthor"
	event.PullRequest.Number = 42

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"alice": {Kind: "review"},
				"bob":   {Kind: "review"},
			},
		},
	}

	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	// Give goroutines time to complete
	time.Sleep(200 * time.Millisecond)

	postedMu.Lock()
	defer postedMu.Unlock()

	if len(postedThreads) == 0 {
		t.Error("expected thread to be posted to channel")
	}
}

// TestHandlePullRequestEventWithData_MultipleChannels tests posting to multiple channels.
func TestHandlePullRequestEventWithData_MultipleChannels(t *testing.T) {
	ctx := context.Background()

	var postedMu sync.Mutex
	postedChannels := []string{}

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
			"qa":          "C_QA",
		}).
		WithBotInChannel(true).
		Build()

	mockSlack.postThreadFunc = func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
		postedMu.Lock()
		postedChannels = append(postedChannels, channelID)
		postedMu.Unlock()
		return "1234.567", nil
	}

	mockSlack.botInfoFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{UserID: "UBOT"}, nil
	}

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering", "qa"}).
		Build()

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		WithUserMapper(NewMockUserMapper().WithDefaultMapping().Build()).
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
		Number: 100,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/100"
	event.PullRequest.Title = "Multi-channel PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "multiauthor"
	event.PullRequest.Number = 100

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"reviewer": {Kind: "review"},
			},
		},
	}

	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	time.Sleep(200 * time.Millisecond)

	postedMu.Lock()
	defer postedMu.Unlock()

	if len(postedChannels) != 2 {
		t.Errorf("expected 2 channels to receive posts, got %d", len(postedChannels))
	}
}

// TestHandlePullRequestEventWithData_DraftPR tests handling of draft PRs.
func TestHandlePullRequestEventWithData_DraftPR(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"drafts": "C_DRAFTS",
		}).
		WithBotInChannel(true).
		WithPostThreadSuccess("1234.567").
		Build()

	mockSlack.botInfoFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{UserID: "UBOT"}, nil
	}

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"drafts"}).
		Build()

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		WithUserMapper(NewMockUserMapper().WithDefaultMapping().Build()).
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
		Number: 200,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/200"
	event.PullRequest.Title = "Draft: WIP feature"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "draftauthor"
	event.PullRequest.Number = 200

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
			Draft: true,
		},
		Analysis: turn.Analysis{
			WorkflowState: "draft",
			NextAction:    map[string]turn.Action{},
		},
	}

	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	time.Sleep(200 * time.Millisecond)

	// Test passes if it doesn't panic - draft PRs should be handled
}

// TestHandlePullRequestEventWithData_MergedState tests handling of merged PRs.
func TestHandlePullRequestEventWithData_MergedState(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"merged": "C_MERGED",
		}).
		WithBotInChannel(true).
		WithPostThreadSuccess("1234.567").
		Build()

	mockSlack.botInfoFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{UserID: "UBOT"}, nil
	}

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"merged"}).
		Build()

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		WithUserMapper(NewMockUserMapper().WithDefaultMapping().Build()).
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
		Number: 300,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/300"
	event.PullRequest.Title = "Merged feature"
	event.PullRequest.CreatedAt = time.Now().Add(-2 * time.Hour)
	event.PullRequest.User.Login = "mergeauthor"
	event.PullRequest.Number = 300

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "closed",
			Merged: true,
		},
		Analysis: turn.Analysis{
			WorkflowState: "merged",
			NextAction:    map[string]turn.Action{},
		},
	}

	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	time.Sleep(200 * time.Millisecond)

	// Test passes - merged PRs should be handled gracefully
}

// TestHandlePullRequestEventWithData_ApprovedState tests when no users are blocked.
func TestHandlePullRequestEventWithData_ApprovedState(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"approved": "C_APPROVED",
		}).
		WithBotInChannel(true).
		WithPostThreadSuccess("1234.567").
		Build()

	mockSlack.botInfoFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{UserID: "UBOT"}, nil
	}

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"approved"}).
		Build()

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		WithUserMapper(NewMockUserMapper().WithDefaultMapping().Build()).
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
		Action: "synchronize",
		Number: 400,
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/400"
	event.PullRequest.Title = "Approved PR"
	event.PullRequest.CreatedAt = time.Now().Add(-1 * time.Hour)
	event.PullRequest.User.Login = "approvedauthor"
	event.PullRequest.Number = 400

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
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

	c.handlePullRequestEventWithData(ctx, "testorg", "testrepo", event, checkResult, nil)

	time.Sleep(200 * time.Millisecond)

	// Test passes - no DMs should be sent when no users are blocked
}
