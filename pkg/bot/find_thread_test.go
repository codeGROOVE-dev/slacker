package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/slack-go/slack"
)

// TestFindPRThread_InMemoryCache tests finding thread in memory cache
func TestFindPRThread_InMemoryCache(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		threadCache: cache.New(),
		stateStore:  state.NewMemoryStore(),
	}

	cacheKey := "org/repo#1:C123"

	// Pre-populate cache
	c.threadCache.Set(cacheKey, cache.ThreadInfo{
		ThreadTS:    "1234.5678",
		ChannelID:   "C123",
		LastState:   "open",
		MessageText: "test message",
	})

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-24 * time.Hour),
	}
	pullRequest.User.Login = "author"
	pullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	pullRequest.Title = "Test PR"
	pullRequest.Number = 1

	threadTS, messageText, found := c.findPRThread(ctx, cacheKey, "C123", "org", "repo", 1, "open", pullRequest)

	if !found {
		t.Error("expected to find thread in cache")
	}

	if threadTS != "1234.5678" {
		t.Errorf("expected threadTS 1234.5678, got %s", threadTS)
	}

	if messageText != "test message" {
		t.Errorf("expected message text 'test message', got %s", messageText)
	}
}

// TestFindPRThread_InDatastore tests finding thread in datastore (cache miss)
func TestFindPRThread_InDatastore(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()

	// Pre-populate datastore
	err := testStore.SaveThread(ctx, "org", "repo", 1, "C123", cache.ThreadInfo{
		ThreadTS:    "1234.5678",
		ChannelID:   "C123",
		LastState:   "open",
		MessageText: "test message",
	})
	if err != nil {
		t.Fatalf("failed to save thread: %v", err)
	}

	c := &Coordinator{
		threadCache: cache.New(),
		stateStore:  testStore,
	}

	cacheKey := "org/repo#1:C123"

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-24 * time.Hour),
	}
	pullRequest.User.Login = "author"
	pullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	pullRequest.Title = "Test PR"
	pullRequest.Number = 1

	threadTS, messageText, found := c.findPRThread(ctx, cacheKey, "C123", "org", "repo", 1, "open", pullRequest)

	if !found {
		t.Error("expected to find thread in datastore")
	}

	if threadTS != "1234.5678" {
		t.Errorf("expected threadTS 1234.5678, got %s", threadTS)
	}

	if messageText != "test message" {
		t.Errorf("expected message text 'test message', got %s", messageText)
	}

	// Verify cache was warmed
	cachedInfo, exists := c.threadCache.Get(cacheKey)
	if !exists {
		t.Error("expected cache to be warmed after datastore hit")
	}
	if cachedInfo.ThreadTS != "1234.5678" {
		t.Errorf("expected cached threadTS 1234.5678, got %s", cachedInfo.ThreadTS)
	}
}

// TestFindPRThread_NotFound tests when thread is not found anywhere
func TestFindPRThread_NotFound(t *testing.T) {
	ctx := context.Background()

	// Mock Slack that returns no search results
	mockSlack := &mockSlackClient{
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{},
			}, nil
		},
	}

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
		stateStore:  state.NewMemoryStore(),
	}

	cacheKey := "org/repo#1:C123"

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-24 * time.Hour),
	}
	pullRequest.User.Login = "author"
	pullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	pullRequest.Title = "Test PR"
	pullRequest.Number = 1

	threadTS, messageText, found := c.findPRThread(ctx, cacheKey, "C123", "org", "repo", 1, "open", pullRequest)

	if found {
		t.Error("expected not to find thread")
	}

	if threadTS != "" {
		t.Errorf("expected empty threadTS, got %s", threadTS)
	}

	if messageText != "" {
		t.Errorf("expected empty messageText, got %s", messageText)
	}
}

// TestFindPRThread_ZeroCreatedAt tests 30-day fallback when CreatedAt is zero
func TestFindPRThread_ZeroCreatedAt(t *testing.T) {
	ctx := context.Background()

	// Mock Slack that returns no search results
	mockSlack := &mockSlackClient{
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{},
			}, nil
		},
	}

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
		stateStore:  state.NewMemoryStore(),
	}

	cacheKey := "org/repo#1:C123"

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Time{}, // Zero value - will trigger 30-day fallback
	}
	pullRequest.User.Login = "author"
	pullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	pullRequest.Title = "Test PR"
	pullRequest.Number = 1

	_, _, found := c.findPRThread(ctx, cacheKey, "C123", "org", "repo", 1, "open", pullRequest)

	if found {
		t.Error("expected not to find thread (testing zero CreatedAt fallback)")
	}
}

// TestFindPRThread_OldPR tests 30-day fallback for old PRs
func TestFindPRThread_OldPR(t *testing.T) {
	ctx := context.Background()

	// Mock Slack that returns no search results
	mockSlack := &mockSlackClient{
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{},
			}, nil
		},
	}

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
		stateStore:  state.NewMemoryStore(),
	}

	cacheKey := "org/repo#1:C123"

	pullRequest := struct {
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		Number  int    `json:"number"`
	}{
		CreatedAt: time.Now().Add(-31 * 24 * time.Hour), // 31 days old - triggers fallback
	}
	pullRequest.User.Login = "author"
	pullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	pullRequest.Title = "Test PR"
	pullRequest.Number = 1

	_, _, found := c.findPRThread(ctx, cacheKey, "C123", "org", "repo", 1, "open", pullRequest)

	if found {
		t.Error("expected not to find thread (testing old PR fallback)")
	}
}
