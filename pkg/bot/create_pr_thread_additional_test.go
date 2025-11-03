package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestCoordinator_CreatePRThread_ChannelResolutionFailure tests error when channel can't be resolved.
func TestCoordinator_CreatePRThread_ChannelResolutionFailure(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// Return the input unchanged to simulate resolution failure
			return channelName
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now(),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	// Use a channel name that doesn't start with C (not a channel ID)
	_, _, err := c.createPRThread(ctx, "nonexistent-channel", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)

	if err == nil {
		t.Error("expected error when channel cannot be resolved")
	}

	if !strings.Contains(err.Error(), "could not resolve channel") {
		t.Errorf("expected error to mention channel resolution, got: %v", err)
	}
}

// TestCoordinator_CreatePRThread_ChannelWithHashPrefix tests channel resolution with # prefix.
func TestCoordinator_CreatePRThread_ChannelWithHashPrefix(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// Simulate stripping # prefix - should return same if resolution fails
			if strings.HasPrefix(channelName, "#") {
				return channelName[1:]
			}
			return channelName
		},
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			return "1234567890.123456", nil
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now(),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	// Use channel name with # prefix - should fail since resolution returns same without C prefix
	_, _, err := c.createPRThread(ctx, "#general", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)

	if err == nil {
		t.Error("expected error when channel with # prefix cannot be resolved to ID")
	}
}

// TestCoordinator_CreatePRThread_ChannelAlreadyID tests when channel is already a channel ID.
func TestCoordinator_CreatePRThread_ChannelAlreadyID(t *testing.T) {
	ctx := context.Background()

	var postedChannelID string
	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			// If it's already a channel ID, return as-is
			return channelName
		},
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			postedChannelID = channelID
			return "1234567890.123456", nil
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now(),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	// Use a channel ID (starts with C)
	threadTS, _, err := c.createPRThread(ctx, "C123456", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if threadTS == "" {
		t.Error("expected non-empty thread timestamp")
	}

	if postedChannelID != "C123456" {
		t.Errorf("expected to post to C123456, got %s", postedChannelID)
	}
}

// TestCoordinator_CreatePRThread_EmptyChannel tests error when channel is empty.
func TestCoordinator_CreatePRThread_EmptyChannel(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		resolveChannelFunc: func(ctx context.Context, channelName string) string {
			return channelName // Return as-is
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now(),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	_, _, err := c.createPRThread(ctx, "", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)

	if err == nil {
		t.Error("expected error when channel is empty")
	}
}
