package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// NOTE: "when" threshold tests are omitted because they require complex YAML config
// mocking. The threshold logic itself (shouldPostThread) is tested separately at 100% coverage.

// TestProcessPRForChannel_InvalidEventTypeAssertionFailure tests invalid event type assertion
func TestProcessPRForChannel_InvalidEventTypeAssertionFailure(t *testing.T) {
	ctx := context.Background()

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(NewMockSlack().Build()).
		WithConfig(NewMockConfig().Build()).
		Build()

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 1,
		State:  "open",
		Event:  "invalid-event-type", // Wrong type
		CheckRes: &turn.CheckResponse{
			PullRequest: prx.PullRequest{State: "open"},
		},
	}

	// Should return nil due to invalid event type
	taggedUsers := c.processPRForChannel(ctx, prCtx, "testrepo", "workspace-123")

	if taggedUsers != nil {
		t.Errorf("expected nil taggedUsers for invalid event type, got %v", taggedUsers)
	}
}

// TestProcessPRForChannel_TerminalStateDMUpdate tests merged/closed PR DM updates
func TestProcessPRForChannel_TerminalStateDMUpdate(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			return "C123"
		},
		botInChannelFunc: func(ctx context.Context, channelID string) bool {
			return true
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			// Return existing thread
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							Timestamp: "1234.5678",
							Text:      ":hourglass: Test PR https://github.com/testorg/testrepo/pull/1?st=awaiting_review",
							User:      "UBOT",
						},
					},
				},
			}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "UBOT"}, nil
		},
		updateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			return nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockState := NewMockState().Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		WithUserMapper(NewMockUserMapper().Build()).
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
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 1,
		State:  "merged", // Terminal state
		Event:  event,
		CheckRes: &turn.CheckResponse{
			PullRequest: prx.PullRequest{
				State:  "closed",
				Merged: true,
			},
		},
	}

	// Process the PR - should update existing thread and trigger DM updates for terminal state
	taggedUsers := c.processPRForChannel(ctx, prCtx, "testrepo", "workspace-123")

	// Thread exists, so taggedUsers should be non-nil
	if taggedUsers == nil {
		t.Error("expected non-nil taggedUsers when processing existing thread")
	}
}

// TestProcessPRForChannel_ChannelResolutionFailure tests when channel can't be resolved
func TestProcessPRForChannel_ChannelResolutionFailure(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// Return same as input (resolution failed)
			return channelName
		},
	}

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(NewMockConfig().Build()).
		Build()
	c.workspaceName = "test-workspace"

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
	}
	event.PullRequest.HTMLURL = "https://github.com/testorg/testrepo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.CreatedAt = time.Now()
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	prCtx := prContext{
		Owner:  "testorg",
		Repo:   "testrepo",
		Number: 1,
		State:  "open",
		Event:  event,
		CheckRes: &turn.CheckResponse{
			PullRequest: prx.PullRequest{State: "open"},
		},
	}

	// Should return nil due to channel resolution failure
	taggedUsers := c.processPRForChannel(ctx, prCtx, "unknown-channel", "workspace-123")

	if taggedUsers != nil {
		t.Errorf("expected nil taggedUsers when channel resolution fails, got %v", taggedUsers)
	}
}
