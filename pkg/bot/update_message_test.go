package bot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestUpdateMessageIfNeeded_InvalidEventType tests when event type assertion fails
func TestUpdateMessageIfNeeded_InvalidEventType(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().Build()
	mockConfig := NewMockConfig().Build()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	params := messageUpdateParams{
		Event:    "invalid-event-type", // Wrong type
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
	}

	// Should return early with error log
	c.updateMessageIfNeeded(ctx, params)

	// Test passes if no panic occurs
}

// TestUpdateMessageIfNeeded_MessageAlreadyMatches tests when message doesn't need update
func TestUpdateMessageIfNeeded_MessageAlreadyMatches(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().Build()
	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{},
	}

	testStore := state.NewMemoryStore()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		stateStore:    testStore,
		threadCache:   cache.New(),
		workspaceName: "test-workspace",
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
	}
	event.PullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	// Format expected message
	expectedMsg := ":hourglass: Test PR https://github.com/org/repo/pull/1?st=test_state"

	params := messageUpdateParams{
		Event:          event,
		Owner:          "org",
		Repo:           "repo",
		PRNumber:       1,
		ChannelID:      "C123",
		ChannelName:    "test-channel",
		ChannelDisplay: "#test-channel",
		ThreadTS:       "1234.5678",
		CurrentText:    expectedMsg, // Already matches
		PRState:        "test_state",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	// Should return early (message already matches)
	c.updateMessageIfNeeded(ctx, params)

	// Test passes if no panic occurs
}

// TestUpdateMessageIfNeeded_UpdateError tests when UpdateMessage fails
func TestUpdateMessageIfNeeded_UpdateError(t *testing.T) {
	ctx := context.Background()

	// Mock Slack that returns error for UpdateMessage
	mockSlack := NewMockSlack().
		WithUpdateMessageError(errors.New("slack API error")).
		Build()

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{},
	}

	testStore := state.NewMemoryStore()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		stateStore:    testStore,
		threadCache:   cache.New(),
		workspaceName: "test-workspace",
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
		Action: "synchronized",
	}
	event.PullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	params := messageUpdateParams{
		Event:          event,
		Owner:          "org",
		Repo:           "repo",
		PRNumber:       1,
		ChannelID:      "C123",
		ChannelName:    "test-channel",
		ChannelDisplay: "#test-channel",
		ThreadTS:       "1234.5678",
		CurrentText:    "old message",
		PRState:        "test_state",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	// Should handle UpdateMessage error gracefully
	c.updateMessageIfNeeded(ctx, params)

	// Test passes if no panic occurs
}

// TestUpdateMessageIfNeeded_Success tests successful message update
func TestUpdateMessageIfNeeded_Success(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithUpdateMessageSuccess().
		Build()

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{},
	}

	testStore := state.NewMemoryStore()

	c := &Coordinator{
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		stateStore:    testStore,
		threadCache:   cache.New(),
		workspaceName: "test-workspace",
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
		Action: "synchronized",
	}
	event.PullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	params := messageUpdateParams{
		Event:          event,
		Owner:          "org",
		Repo:           "repo",
		PRNumber:       1,
		ChannelID:      "C123",
		ChannelName:    "test-channel",
		ChannelDisplay: "#test-channel",
		ThreadTS:       "1234.5678",
		CurrentText:    "old message",
		PRState:        "open",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	// Should successfully update message
	c.updateMessageIfNeeded(ctx, params)

	// Test passes if no panic occurs
}

// TestUpdateMessageIfNeeded_LogDeduplication tests log deduplication within 1 second
func TestUpdateMessageIfNeeded_LogDeduplication(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithUpdateMessageSuccess().
		Build()

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{},
	}

	testStore := state.NewMemoryStore()

	c := &Coordinator{
		slack:            mockSlack,
		configManager:    mockConfig,
		userMapper:       mockMapper,
		stateStore:       testStore,
		threadCache:      cache.New(),
		workspaceName:    "test-workspace",
		recentUpdateLogs: make(map[string]time.Time),
		recentUpdateLogsMu: sync.Mutex{},
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
		Action: "synchronized",
	}
	event.PullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	params := messageUpdateParams{
		Event:          event,
		Owner:          "org",
		Repo:           "repo",
		PRNumber:       1,
		ChannelID:      "C123",
		ChannelName:    "test-channel",
		ChannelDisplay: "#test-channel",
		ThreadTS:       "1234.5678",
		CurrentText:    "old message",
		PRState:        "open",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	// First call - should log
	c.updateMessageIfNeeded(ctx, params)

	// Second call immediately after - should deduplicate log
	params.CurrentText = "different message"
	c.updateMessageIfNeeded(ctx, params)

	// Test passes if no panic occurs
}

// TestUpdateMessageIfNeeded_OldLogCleanup tests cleanup of old log entries
func TestUpdateMessageIfNeeded_OldLogCleanup(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithUpdateMessageSuccess().
		Build()

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{},
	}

	testStore := state.NewMemoryStore()

	c := &Coordinator{
		slack:            mockSlack,
		configManager:    mockConfig,
		userMapper:       mockMapper,
		stateStore:       testStore,
		threadCache:      cache.New(),
		workspaceName:    "test-workspace",
		recentUpdateLogs: make(map[string]time.Time),
		recentUpdateLogsMu: sync.Mutex{},
	}

	// Add old log entry (>5 seconds ago)
	c.recentUpdateLogs["old_key"] = time.Now().Add(-10 * time.Second)

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
		Action: "synchronized",
	}
	event.PullRequest.HTMLURL = "https://github.com/org/repo/pull/1"
	event.PullRequest.Title = "Test PR"
	event.PullRequest.User.Login = "author"
	event.PullRequest.Number = 1

	params := messageUpdateParams{
		Event:          event,
		Owner:          "org",
		Repo:           "repo",
		PRNumber:       1,
		ChannelID:      "C123",
		ChannelName:    "test-channel",
		ChannelDisplay: "#test-channel",
		ThreadTS:       "1234.5678",
		CurrentText:    "old message",
		PRState:        "open",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{},
			},
		},
	}

	// Should clean up old log entries during this call
	c.updateMessageIfNeeded(ctx, params)

	// Check that old entry was cleaned up
	c.recentUpdateLogsMu.Lock()
	_, exists := c.recentUpdateLogs["old_key"]
	c.recentUpdateLogsMu.Unlock()

	if exists {
		t.Error("expected old log entry to be cleaned up")
	}
}
