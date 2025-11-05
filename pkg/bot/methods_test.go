package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/slack-go/slack"
)

// testCoordinator creates a coordinator with mocks for testing.
func testCoordinator(mockState *mockStateStore) *Coordinator {
	return &Coordinator{
		slack:          &mockSlackClient{},
		stateStore:     mockState,
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}
}

func TestCoordinator_SaveThread(t *testing.T) {
	ctx := context.Background()
	mockState := &mockStateStore{
		threads: make(map[string]cache.ThreadInfo),
	}
	c := testCoordinator(mockState)

	owner := "testorg"
	repo := "testrepo"
	number := 42
	channelID := "C123456"

	info := cache.ThreadInfo{
		ThreadTS:      "1234567890.123456",
		MessageText:   "Test PR #42",
		ChannelID:     channelID,
		LastState:     "awaiting_review",
		LastEventTime: time.Now(),
	}

	c.saveThread(ctx, owner, repo, number, channelID, info)

	// Verify saved to cache
	cacheKey := "testorg/testrepo#42:C123456"
	cachedInfo, exists := c.threadCache.Get(cacheKey)
	if !exists {
		t.Fatal("expected thread to be saved in cache")
	}

	if cachedInfo.ThreadTS != info.ThreadTS {
		t.Errorf("expected ThreadTS %s, got %s", info.ThreadTS, cachedInfo.ThreadTS)
	}

	if cachedInfo.MessageText != info.MessageText {
		t.Errorf("expected MessageText %s, got %s", info.MessageText, cachedInfo.MessageText)
	}

	if cachedInfo.LastState != info.LastState {
		t.Errorf("expected LastState %s, got %s", info.LastState, cachedInfo.LastState)
	}

	// Give the goroutine time to persist
	time.Sleep(10 * time.Millisecond)

	// Verify saved to state store
	storeKey := fmt.Sprintf("thread:%s/%s#%d:%s", owner, repo, number, channelID)
	if _, ok := mockState.threads[storeKey]; !ok {
		t.Error("expected thread to be saved in state store")
	}
}

func TestCoordinator_SearchForPRThread_Found(t *testing.T) {
	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	mockSlack := &mockSlackClient{
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							User:      "U999",
							Text:      "Some other message",
							Timestamp: "1234567890.123456",
						},
					},
					{
						Msg: slack.Msg{
							User:      "B123", // Bot user
							Text:      ":hourglass: Fix bug • testorg/testrepo#42 by @author " + prURL + "?st=awaiting_review",
							Timestamp: "1234567890.123457",
						},
					},
				},
			}, nil
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	threadTS, messageText := c.searchForPRThread(ctx, "C123", prURL, time.Now().Add(-24*time.Hour))

	if threadTS == "" {
		t.Fatal("expected to find thread")
	}

	if threadTS != "1234567890.123457" {
		t.Errorf("expected threadTS 1234567890.123457, got %s", threadTS)
	}

	if !strings.Contains(messageText, prURL) {
		t.Errorf("expected message text to contain %s, got %s", prURL, messageText)
	}
}

func TestCoordinator_SearchForPRThread_NotFound(t *testing.T) {
	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	mockSlack := &mockSlackClient{
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return &slack.GetConversationHistoryResponse{
				Messages: []slack.Message{
					{
						Msg: slack.Msg{
							User:      "U999",
							Text:      "Some other message",
							Timestamp: "1234567890.123456",
						},
					},
				},
			}, nil
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	threadTS, messageText := c.searchForPRThread(ctx, "C123", prURL, time.Now().Add(-24*time.Hour))

	if threadTS != "" {
		t.Errorf("expected empty threadTS, got %s", threadTS)
	}

	if messageText != "" {
		t.Errorf("expected empty message text, got %s", messageText)
	}
}

func TestCoordinator_SearchForPRThread_BotInfoError(t *testing.T) {
	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	mockSlack := &mockSlackClient{
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return nil, errors.New("API error")
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	threadTS, messageText := c.searchForPRThread(ctx, "C123", prURL, time.Now().Add(-24*time.Hour))

	// Should gracefully return empty values
	if threadTS != "" {
		t.Errorf("expected empty threadTS on error, got %s", threadTS)
	}

	if messageText != "" {
		t.Errorf("expected empty message text on error, got %s", messageText)
	}
}

func TestCoordinator_SearchForPRThread_HistoryError(t *testing.T) {
	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	mockSlack := &mockSlackClient{
		botInfoFunc: func(ctx context.Context) (*slack.AuthTestResponse, error) {
			return &slack.AuthTestResponse{UserID: "B123"}, nil
		},
		channelHistoryFunc: func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
			return nil, errors.New("channel not found")
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)
	c.slack = mockSlack

	threadTS, messageText := c.searchForPRThread(ctx, "C123", prURL, time.Now().Add(-24*time.Hour))

	// Should gracefully return empty values
	if threadTS != "" {
		t.Errorf("expected empty threadTS on error, got %s", threadTS)
	}

	if messageText != "" {
		t.Errorf("expected empty message text on error, got %s", messageText)
	}
}

func TestCoordinator_CreatePRThread(t *testing.T) {
	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	var postedChannelID, postedText string
	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			postedChannelID = channelID
			postedText = text
			return "1234567890.999999", nil
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
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   prURL,
		Title:     "Fix critical bug",
		Number:    42,
	}
	pr.User.Login = "testauthor"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	threadTS, messageText, err := c.createPRThread(ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if threadTS != "1234567890.999999" {
		t.Errorf("expected threadTS 1234567890.999999, got %s", threadTS)
	}

	if postedChannelID != "C123" {
		t.Errorf("expected to post to C123, got %s", postedChannelID)
	}

	if !strings.Contains(postedText, "Fix critical bug") {
		t.Errorf("expected posted text to contain PR title, got %s", postedText)
	}

	if !strings.Contains(postedText, prURL) {
		t.Errorf("expected posted text to contain PR URL, got %s", postedText)
	}

	if messageText != postedText {
		t.Errorf("expected returned message text to match posted text")
	}
}

func TestCoordinator_CreatePRThread_PostError(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		postThreadFunc: func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
			return "", errors.New("failed to post message")
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
		CreatedAt: time.Now().Add(-1 * time.Hour),
		HTMLURL:   "https://github.com/testorg/testrepo/pull/42",
		Title:     "Fix bug",
		Number:    42,
	}
	pr.User.Login = "author"

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			WorkflowState: "awaiting_review",
		},
	}

	_, _, err := c.createPRThread(ctx, "C123", "testorg", "testrepo", 42, "awaiting_review", pr, checkResult)

	if err == nil {
		t.Error("expected error when posting thread fails")
	}

	if !strings.Contains(err.Error(), "failed to post message") {
		t.Errorf("expected error message to mention post failure, got: %v", err)
	}
}

// TestCoordinator_ExtractStateFromTurnclient is tested through integration tests
// since creating valid turn.CheckResponse structs requires internal types from turnclient

func TestCoordinator_ExtractBlockedUsersFromTurnclient(t *testing.T) {
	tests := []struct {
		name            string
		checkResult     *turn.CheckResponse
		expectedUserCnt int
	}{
		{
			name: "blocked users in NextAction",
			checkResult: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{
						"alice":   {},
						"bob":     {},
						"_system": {}, // Should be filtered out
					},
				},
			},
			expectedUserCnt: 2, // alice and bob, not _system
		},
		{
			name: "no blocked users",
			checkResult: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{},
				},
			},
			expectedUserCnt: 0,
		},
		{
			name: "only _system (filtered out)",
			checkResult: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{
						"_system": {},
					},
				},
			},
			expectedUserCnt: 0,
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := c.extractBlockedUsersFromTurnclient(tt.checkResult)

			if len(users) != tt.expectedUserCnt {
				t.Errorf("expected %d users, got %d", tt.expectedUserCnt, len(users))
			}

			// Verify _system is never included
			for _, user := range users {
				if user == "_system" {
					t.Error("_system should be filtered out from blocked users")
				}
			}
		})
	}
}

func TestCoordinator_GetStateQueryParam(t *testing.T) {
	tests := []struct {
		name          string
		state         string
		expectedParam string
	}{
		{
			name:          "awaiting review",
			state:         "awaiting_review",
			expectedParam: "?st=awaiting_review",
		},
		{
			name:          "tests broken",
			state:         "tests_broken",
			expectedParam: "?st=tests_broken",
		},
		{
			name:          "reviewed and approved",
			state:         "approved",
			expectedParam: "?st=approved",
		},
		{
			name:          "empty state",
			state:         "",
			expectedParam: "",
		},
	}

	mockState := &mockStateStore{}
	c := testCoordinator(mockState)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			param := c.getStateQueryParam(tt.state)

			if param != tt.expectedParam {
				t.Errorf("expected param %s, got %s", tt.expectedParam, param)
			}
		})
	}
}
