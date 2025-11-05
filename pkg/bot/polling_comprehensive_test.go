package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/slack-go/slack"
)

// TestUpdateClosedPRThread_ThreadInStateStore tests updating when thread is in state store.
func TestUpdateClosedPRThread_ThreadInStateStore(t *testing.T) {
	ctx := context.Background()

	updateCalled := false
	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
		}).
		Build()

	mockSlack.updateMessageFunc = func(ctx context.Context, channelID, timestamp, text string) error {
		updateCalled = true
		if channelID != "C_ENG" {
			t.Errorf("expected channel C_ENG, got %s", channelID)
		}
		if timestamp != "1234.567" {
			t.Errorf("expected timestamp 1234.567, got %s", timestamp)
		}
		return nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering"}).
		Build()

	mockState := NewMockState().
		WithThread("testorg", "testrepo", 42, "C_ENG", cache.ThreadInfo{
			ThreadTS:    "1234.567",
			ChannelID:   "C_ENG",
			MessageText: "old message",
			UpdatedAt:   time.Now().Add(-1 * time.Hour),
		}).
		Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    42,
		State:     "MERGED",
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Author:    "testauthor",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !updateCalled {
		t.Error("expected UpdateMessage to be called")
	}
}

// TestUpdateClosedPRThread_ThreadFoundViaChannelHistory tests fallback to channel history search.
func TestUpdateClosedPRThread_ThreadFoundViaChannelHistory(t *testing.T) {
	ctx := context.Background()

	updateCalled := false
	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
		}).
		Build()

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		// Return a message that matches the PR URL - must be from the bot
		return &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{
				{
					Msg: slack.Msg{
						Timestamp: "1234.567",
						User:      "B123", // Must match BotInfo UserID
						Text:      ":rocket: Test PR <https://github.com/testorg/testrepo/pull/42|testrepo#42>",
					},
				},
			},
		}, nil
	}

	mockSlack.botInfoFunc = func(ctx context.Context) (*slack.AuthTestResponse, error) {
		return &slack.AuthTestResponse{UserID: "B123"}, nil
	}

	mockSlack.updateMessageFunc = func(ctx context.Context, channelID, timestamp, text string) error {
		updateCalled = true
		return nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering"}).
		Build()

	// State store doesn't have the thread
	mockState := NewMockState().Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    42,
		State:     "MERGED",
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Author:    "testauthor",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !updateCalled {
		t.Error("expected UpdateMessage to be called after finding thread via history")
	}
}

// TestUpdateClosedPRThread_ThreadNotFoundAnywhere tests when thread doesn't exist.
func TestUpdateClosedPRThread_ThreadNotFoundAnywhere(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
		}).
		Build()

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		// Return empty history
		return &slack.GetConversationHistoryResponse{
			Messages: []slack.Message{},
		}, nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering"}).
		Build()

	mockState := NewMockState().Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    99,
		State:     "CLOSED",
		URL:       "https://github.com/testorg/testrepo/pull/99",
		Title:     "Never posted PR",
		Author:    "testauthor",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	// Should not error when thread isn't found - just log and continue
	if err != nil && err.Error() != "no threads found or updated for closed PR" {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestUpdateClosedPRThread_ChannelHistoryError tests handling of channel history API errors.
func TestUpdateClosedPRThread_ChannelHistoryError(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
		}).
		Build()

	mockSlack.channelHistoryFunc = func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
		return nil, errors.New("slack API error")
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering"}).
		Build()

	mockState := NewMockState().Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    50,
		State:     "CLOSED",
		URL:       "https://github.com/testorg/testrepo/pull/50",
		Title:     "Error test PR",
		Author:    "testauthor",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	// Should handle errors gracefully
	if err != nil && err.Error() != "no threads found or updated for closed PR" {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestUpdateClosedPRThread_UpdateMessageError tests handling of update failures.
func TestUpdateClosedPRThread_UpdateMessageError(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
		}).
		Build()

	mockSlack.updateMessageFunc = func(ctx context.Context, channelID, timestamp, text string) error {
		return errors.New("slack update failed")
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering"}).
		Build()

	mockState := NewMockState().
		WithThread("testorg", "testrepo", 42, "C_ENG", cache.ThreadInfo{
			ThreadTS:    "1234.567",
			ChannelID:   "C_ENG",
			MessageText: "old message",
			UpdatedAt:   time.Now().Add(-1 * time.Hour),
		}).
		Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    42,
		State:     "MERGED",
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Title:     "Test PR",
		Author:    "testauthor",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	// When all updates fail, function returns "no threads found or updated" error
	if err == nil {
		t.Error("expected error when all thread updates fail")
	} else if err.Error() != "no threads found or updated for closed PR" {
		t.Errorf("expected 'no threads found or updated' error, got: %v", err)
	}
}

// TestUpdateClosedPRThread_MultipleChannels tests updating threads across multiple channels.
func TestUpdateClosedPRThread_MultipleChannels(t *testing.T) {
	ctx := context.Background()

	updateCount := 0
	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
			"qa":          "C_QA",
		}).
		Build()

	mockSlack.updateMessageFunc = func(ctx context.Context, channelID, timestamp, text string) error {
		updateCount++
		return nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering", "qa"}).
		Build()

	mockState := NewMockState().
		WithThread("testorg", "testrepo", 42, "C_ENG", cache.ThreadInfo{
			ThreadTS:  "1111.111",
			ChannelID: "C_ENG",
		}).
		WithThread("testorg", "testrepo", 42, "C_QA", cache.ThreadInfo{
			ThreadTS:  "2222.222",
			ChannelID: "C_QA",
		}).
		Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    42,
		State:     "MERGED",
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Title:     "Multi-channel PR",
		Author:    "testauthor",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if updateCount != 2 {
		t.Errorf("expected 2 updates (one per channel), got %d", updateCount)
	}
}

// TestUpdateClosedPRThread_ClosedNotMerged tests updating for closed (not merged) PRs.
func TestUpdateClosedPRThread_ClosedNotMerged(t *testing.T) {
	ctx := context.Background()

	var capturedText string
	mockSlack := NewMockSlack().
		WithChannelResolutionMap(map[string]string{
			"engineering": "C_ENG",
		}).
		Build()

	mockSlack.updateMessageFunc = func(ctx context.Context, channelID, timestamp, text string) error {
		capturedText = text
		return nil
	}

	mockConfig := NewMockConfig().
		WithChannels("testorg", "testrepo", []string{"engineering"}).
		Build()

	mockState := NewMockState().
		WithThread("testorg", "testrepo", 42, "C_ENG", cache.ThreadInfo{
			ThreadTS:  "1234.567",
			ChannelID: "C_ENG",
		}).
		Build()

	c := NewTestCoordinator().
		WithState(mockState).
		WithSlack(mockSlack).
		WithConfig(mockConfig).
		Build()

	pr := &github.PRSnapshot{
		Owner:     "testorg",
		Repo:      "testrepo",
		Number:    42,
		State:     "CLOSED",
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Title:     "Closed without merge",
		Author:    "testauthor",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now(),
	}

	err := c.updateClosedPRThread(ctx, pr)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should use :x: emoji for closed (not merged) PRs
	if capturedText == "" {
		t.Error("expected message to be updated with closed state")
	}
}
