package bot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestCreatePRThread_AsyncEnrichmentSuccess tests the async enrichment path
func TestCreatePRThread_AsyncEnrichmentSuccess(t *testing.T) {
	ctx := context.Background()

	var updateCalled sync.WaitGroup
	updateCalled.Add(1)

	var updatedChannel, updatedTS, updatedText string

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			return "1234.5678", nil
		},
		updateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			updatedChannel = channelID
			updatedTS = ts
			updatedText = text
			updateCalled.Done()
			return nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		stateStore:    state.NewMemoryStore(),
		threadCache:   cache.New(),
	}

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/org/repo/pull/1",
		Title:     "Test PR",
		Number:    1,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Merged: false,
			Draft:  false,
		},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
			},
		},
	}

	threadTS, messageText, err := c.createPRThread(ctx, threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if threadTS != "1234.5678" {
		t.Errorf("expected threadTS 1234.5678, got %s", threadTS)
	}

	// Wait for async enrichment to complete (with timeout)
	done := make(chan struct{})
	go func() {
		updateCalled.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - enrichment completed
	case <-time.After(2 * time.Second):
		t.Fatal("async enrichment did not complete within 2 seconds")
	}

	// Verify UpdateMessage was called
	if updatedChannel != "C123" {
		t.Errorf("expected update to channel C123, got %s", updatedChannel)
	}

	if updatedTS != "1234.5678" {
		t.Errorf("expected update to thread 1234.5678, got %s", updatedTS)
	}

	// Verify enrichment added content to the message
	if !strings.Contains(updatedText, messageText) {
		t.Errorf("expected updated text to contain initial message")
	}

	// Should have added user mentions
	if len(updatedText) <= len(messageText) {
		t.Errorf("expected enriched text to be longer than initial text")
	}
}

// TestCreatePRThread_AsyncEnrichmentUpdateError tests error handling in async enrichment
func TestCreatePRThread_AsyncEnrichmentUpdateError(t *testing.T) {
	ctx := context.Background()

	var updateCalled sync.WaitGroup
	updateCalled.Add(1)

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			return "1234.5678", nil
		},
		updateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			updateCalled.Done()
			return errors.New("slack API error")
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		stateStore:    state.NewMemoryStore(),
		threadCache:   cache.New(),
	}

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/org/repo/pull/1",
		Title:     "Test PR",
		Number:    1,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Merged: false,
			Draft:  false,
		},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
			},
		},
	}

	threadTS, _, err := c.createPRThread(ctx, threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if threadTS != "1234.5678" {
		t.Errorf("expected threadTS 1234.5678, got %s", threadTS)
	}

	// Wait for async enrichment to attempt update (with timeout)
	done := make(chan struct{})
	go func() {
		updateCalled.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - update was attempted (and failed as expected)
	case <-time.After(2 * time.Second):
		t.Fatal("async enrichment did not attempt update within 2 seconds")
	}

	// Test passes if no panic occurred during error handling
}

// TestCreatePRThread_NoNextAction tests thread creation without NextAction (no enrichment)
func TestCreatePRThread_NoNextAction(t *testing.T) {
	ctx := context.Background()

	var postCalled bool

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			postCalled = true
			return "1234.5678", nil
		},
		updateMessageFunc: func(ctx context.Context, channelID, ts, text string) error {
			t.Error("UpdateMessage should not be called when there's no NextAction")
			return nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{},
	}

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		stateStore:    state.NewMemoryStore(),
		threadCache:   cache.New(),
	}

	pr := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/org/repo/pull/1",
		Title:     "Test PR",
		Number:    1,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "open",
			Merged: false,
			Draft:  false,
		},
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
			NextAction:    map[string]turn.Action{}, // Empty - no enrichment needed
		},
	}

	threadTS, _, err := c.createPRThread(ctx, threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !postCalled {
		t.Error("expected PostThread to be called")
	}

	if threadTS != "1234.5678" {
		t.Errorf("expected threadTS 1234.5678, got %s", threadTS)
	}

	// Give goroutine time to start (if it incorrectly starts)
	time.Sleep(100 * time.Millisecond)

	// Test passes if UpdateMessage was never called
}
