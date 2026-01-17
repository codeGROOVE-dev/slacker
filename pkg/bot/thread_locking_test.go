package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestCreatePRThreadWithLocking_SuccessfulCreation tests the happy path
func TestCreatePRThreadWithLocking_SuccessfulCreation(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			return "1234.5678", nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    &mockUserMapper{mapping: map[string]string{}},
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
			NextAction:    map[string]turn.Action{},
		},
	}

	params := threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	info, wasCreated, err := c.createPRThreadWithLocking(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wasCreated {
		t.Error("expected wasCreated to be true")
	}

	if info.ThreadTS != "1234.5678" {
		t.Errorf("expected threadTS 1234.5678, got %s", info.ThreadTS)
	}

	// Verify cache was populated
	cacheKey := "org/repo#1:C123"
	if cachedInfo, exists := c.threadCache.Get(cacheKey); !exists {
		t.Error("expected thread to be cached")
	} else if cachedInfo.ThreadTS != "1234.5678" {
		t.Errorf("expected cached threadTS 1234.5678, got %s", cachedInfo.ThreadTS)
	}
}

// TestCreatePRThreadWithLocking_CacheHitAfterMarking tests finding thread in cache after marking as creating
func TestCreatePRThreadWithLocking_CacheHitAfterMarking(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			t.Error("PostThread should not be called when thread exists in cache")
			return "", errors.New("should not be called")
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	threadCache := cache.New()
	cacheKey := "org/repo#1:C123"

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    &mockUserMapper{mapping: map[string]string{}},
		stateStore:    state.NewMemoryStore(),
		threadCache:   threadCache,
	}

	// Pre-populate cache AFTER we would mark as creating (simulating race)
	// We'll do this by starting the test, then using a custom cache that sets it
	existingInfo := cache.ThreadInfo{
		ThreadTS:    "existing.thread",
		ChannelID:   "C123",
		LastState:   "open",
		MessageText: "existing message",
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
			NextAction:    map[string]turn.Action{},
		},
	}

	params := threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	// Set the cache entry to simulate another goroutine creating it
	threadCache.Set(cacheKey, existingInfo)

	info, wasCreated, err := c.createPRThreadWithLocking(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wasCreated {
		t.Error("expected wasCreated to be false when thread exists in cache")
	}

	if info.ThreadTS != "existing.thread" {
		t.Errorf("expected existing threadTS, got %s", info.ThreadTS)
	}
}

// TestCreatePRThreadWithLocking_CrossInstanceRace tests detecting thread created by another instance
func TestCreatePRThreadWithLocking_CrossInstanceRace(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			t.Error("PostThread should not be called when cross-instance thread detected")
			return "", errors.New("should not be called")
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			// Return an existing message that matches the PR URL
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							Timestamp: "cross.instance",
							Text:      ":hourglass: Test PR https://github.com/org/repo/pull/1",
							User:      "B123", // Bot user
						},
					},
				},
			}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    &mockUserMapper{mapping: map[string]string{}},
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
			NextAction:    map[string]turn.Action{},
		},
	}

	params := threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	info, wasCreated, err := c.createPRThreadWithLocking(ctx, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wasCreated {
		t.Error("expected wasCreated to be false when cross-instance thread detected")
	}

	if info.ThreadTS != "cross.instance" {
		t.Errorf("expected cross-instance threadTS, got %s", info.ThreadTS)
	}
}

// TestCreatePRThreadWithLocking_CreateThreadError tests error handling during thread creation
func TestCreatePRThreadWithLocking_CreateThreadError(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			return "", errors.New("slack API error")
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{Messages: []slack.Message{}}, nil
		},
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    &mockUserMapper{mapping: map[string]string{}},
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
			NextAction:    map[string]turn.Action{},
		},
	}

	params := threadCreationParams{
		ChannelID:   "C123",
		ChannelName: "test-channel",
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRState:     "awaiting_review",
		PullRequest: pr,
		CheckResult: checkResult,
	}

	_, wasCreated, err := c.createPRThreadWithLocking(ctx, params)
	if err == nil {
		t.Fatal("expected error when thread creation fails")
	}

	if wasCreated {
		t.Error("expected wasCreated to be false when creation fails")
	}

	if !errors.Is(err, errors.New("slack API error")) && !contains(err.Error(), "slack API error") {
		t.Errorf("expected error to mention slack API error, got: %v", err)
	}
}
