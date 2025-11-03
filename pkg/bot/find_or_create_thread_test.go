package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// TestFindOrCreatePRThread_CacheHit tests when thread exists in cache.
func TestFindOrCreatePRThread_CacheHit(t *testing.T) {
	ctx := context.Background()

	threadCache := cache.New()
	// Pre-populate cache
	threadCache.Set("testorg/testrepo#42:C123", cache.ThreadInfo{
		ThreadTS:    "1234.567",
		ChannelID:   "C123",
		LastState:   "awaiting_review",
		MessageText: "Existing message",
		UpdatedAt:   time.Now(),
	})

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(NewMockSlack().Build()).
		WithConfig(config.New()).
		Build()
	c.threadCache = threadCache

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pullRequest.User.Login = "testauthor"

	threadTS, wasNew, messageText, err := c.findOrCreatePRThread(ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pullRequest, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wasNew {
		t.Error("expected existing thread, got new")
	}
	if threadTS != "1234.567" {
		t.Errorf("expected threadTS 1234.567, got %s", threadTS)
	}
	if messageText != "Existing message" {
		t.Errorf("expected message text 'Existing message', got %s", messageText)
	}
}

// TestFindOrCreatePRThread_FallbackSearchDate tests 30-day fallback for old PRs.
func TestFindOrCreatePRThread_FallbackSearchDate(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolution("C123", "C123").
		WithBotInChannel(true).
		WithPostThreadSuccess("1234.567").
		Build()

	// Mock channel history to return no messages (no existing thread)
	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{},
		}, nil
	}

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(config.New()).
		Build()

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-60 * 24 * time.Hour), // 60 days ago
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pullRequest.User.Login = "testauthor"

	checkResult := &turn.CheckResponse{}

	threadTS, wasNew, _, err := c.findOrCreatePRThread(ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pullRequest, checkResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wasNew {
		t.Error("expected new thread for old PR")
	}
	if threadTS == "" {
		t.Error("expected threadTS to be set")
	}
}

// TestFindOrCreatePRThread_ConcurrentCreation tests concurrent thread creation protection.
func TestFindOrCreatePRThread_ConcurrentCreation(t *testing.T) {
	ctx := context.Background()

	threadCache := cache.New()
	// Mark as being created by another goroutine
	threadCache.MarkCreating("testorg/testrepo#42:C123")

	mockSlack := NewMockSlack().
		WithChannelResolution("C123", "C123").
		WithBotInChannel(true).
		WithPostThreadSuccess("1234.567").
		Build()

	// Mock channel history
	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{},
		}, nil
	}

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(config.New()).
		Build()
	c.threadCache = threadCache

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pullRequest.User.Login = "testauthor"

	// Start goroutine that will complete the creation after a delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		threadCache.Set("testorg/testrepo#42:C123", cache.ThreadInfo{
			ThreadTS:    "completed.thread",
			ChannelID:   "C123",
			LastState:   "awaiting_review",
			MessageText: "Thread created by other goroutine",
			UpdatedAt:   time.Now(),
		})
		threadCache.UnmarkCreating("testorg/testrepo#42:C123")
	}()

	threadTS, wasNew, _, err := c.findOrCreatePRThread(ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pullRequest, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wasNew {
		t.Error("expected existing thread (created by other goroutine)")
	}
	if threadTS != "completed.thread" {
		t.Errorf("expected threadTS 'completed.thread', got %s", threadTS)
	}
	// Message text may be empty when waiting for concurrent creation
}

// TestFindOrCreatePRThread_ZeroCreatedAt tests handling when CreatedAt is zero.
func TestFindOrCreatePRThread_ZeroCreatedAt(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolution("C123", "C123").
		WithBotInChannel(true).
		WithPostThreadSuccess("1234.567").
		Build()

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{},
		}, nil
	}

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(config.New()).
		Build()

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Time{}, // Zero value
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Number:    42,
	}
	pullRequest.User.Login = "testauthor"

	checkResult := &turn.CheckResponse{}

	threadTS, wasNew, _, err := c.findOrCreatePRThread(ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pullRequest, checkResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wasNew {
		t.Error("expected new thread when CreatedAt is zero")
	}
	if threadTS == "" {
		t.Error("expected threadTS to be set")
	}
}

// TestFindOrCreatePRThread_ExistingThreadFound tests when searchForPRThread finds an existing thread.
func TestFindOrCreatePRThread_ExistingThreadFound(t *testing.T) {
	ctx := context.Background()

	prURL := "https://github.com/testorg/testrepo/pull/42"

	mockSlack := NewMockSlack().Build()
	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{
				{
					Msg: slack.Msg{
						Timestamp: "existing.thread",
						Text:      ":hourglass: Test PR " + prURL,
						User:      "B123", // Must be bot user to be recognized
					},
				},
			},
		}, nil
	}
	mockSlack.botInfoFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{
			UserID: "B123",
		}, nil
	}

	c := NewTestCoordinator().
		WithState(NewMockState().Build()).
		WithSlack(mockSlack).
		WithConfig(config.New()).
		Build()

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   prURL,
		Title:     "Test PR",
		Number:    42,
	}
	pullRequest.User.Login = "testauthor"

	checkResult := &turn.CheckResponse{}

	threadTS, wasNew, messageText, err := c.findOrCreatePRThread(ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pullRequest, checkResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wasNew {
		t.Error("expected existing thread to be found")
	}
	if threadTS != "existing.thread" {
		t.Errorf("expected threadTS 'existing.thread', got %s", threadTS)
	}
	if messageText == "" {
		t.Error("expected message text to be populated")
	}
}
