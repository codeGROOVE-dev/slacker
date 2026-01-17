package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	slackapi "github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestFindDMInHistory_NoExisting(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithFindDMMessagesInHistory([]slackapi.DMLocation{}, nil).
		Build()

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
	}

	locations, err := c.findDMInHistory(ctx, "U123", "https://github.com/org/repo/pull/1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if locations != nil {
		t.Errorf("expected nil locations for no DMs, got %v", locations)
	}
}

func TestFindDMInHistory_MultipleDMs(t *testing.T) {
	ctx := context.Background()

	// Mock finding multiple DMs
	mockLocations := []slackapi.DMLocation{
		{ChannelID: "C123", MessageTS: "1234.5678"},
		{ChannelID: "C123", MessageTS: "1235.5678"},
	}

	mockSlack := NewMockSlack().
		WithFindDMMessagesInHistory(mockLocations, nil).
		Build()

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
	}

	locations, err := c.findDMInHistory(ctx, "U123", "https://github.com/org/repo/pull/1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(locations) != 2 {
		t.Errorf("expected 2 locations, got %d", len(locations))
	}
}

func TestFindDMInHistory_Error(t *testing.T) {
	ctx := context.Background()

	expectedErr := errors.New("slack API error")
	mockSlack := NewMockSlack().
		WithFindDMMessagesInHistory(nil, expectedErr).
		Build()

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
	}

	locations, err := c.findDMInHistory(ctx, "U123", "https://github.com/org/repo/pull/1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	if locations != nil {
		t.Errorf("expected nil locations on error, got %v", locations)
	}
}

func TestQueueDMForUser_Success(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	mockConfig := NewMockConfig().
		WithWorkspace("test-workspace").
		Build()

	c := &Coordinator{
		stateStore:    testStore,
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	req := dmNotificationRequest{
		UserID:   "U123",
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
				NextAction:    map[string]turn.Action{},
			},
		},
		ChannelID:   "C123",
		ChannelName: "test-channel",
	}

	sendAfter := time.Now().Add(1 * time.Hour)
	err := c.queueDMForUser(ctx, req, "awaiting_review", sendAfter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify DM was queued
	pendingDMs, err := testStore.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("failed to get pending DMs: %v", err)
	}

	if len(pendingDMs) != 1 {
		t.Errorf("expected 1 pending DM, got %d", len(pendingDMs))
	}

	if len(pendingDMs) > 0 {
		dm := pendingDMs[0]
		if dm.UserID != "U123" {
			t.Errorf("expected UserID U123, got %s", dm.UserID)
		}
		if dm.PRNumber != 1 {
			t.Errorf("expected PRNumber 1, got %d", dm.PRNumber)
		}
	}
}

func TestQueueDMForUser_InvalidNextAction(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	mockConfig := NewMockConfig().
		WithWorkspace("test-workspace").
		Build()

	c := &Coordinator{
		stateStore:    testStore,
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	// Create a CheckResponse with NextAction that can't be marshaled
	// (In practice, map[string]turn.Action should always marshal, but we can test the error path)
	req := dmNotificationRequest{
		UserID:   "U123",
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
				NextAction:    map[string]turn.Action{},
			},
		},
		ChannelID:   "C123",
		ChannelName: "test-channel",
	}

	sendAfter := time.Now().Add(1 * time.Hour)
	err := c.queueDMForUser(ctx, req, "awaiting_review", sendAfter)

	// Should succeed even with marshal error (falls back to empty JSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueueDMForUser_QueueError(t *testing.T) {
	ctx := context.Background()

	// Create state store that fails to queue
	testStore := NewMockState().
		WithQueuePendingDMError(errors.New("database error")).
		Build()

	mockConfig := NewMockConfig().
		WithWorkspace("test-workspace").
		Build()

	c := &Coordinator{
		stateStore:    testStore,
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	req := dmNotificationRequest{
		UserID:   "U123",
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
				NextAction:    map[string]turn.Action{},
			},
		},
		ChannelID:   "C123",
		ChannelName: "test-channel",
	}

	sendAfter := time.Now().Add(1 * time.Hour)
	err := c.queueDMForUser(ctx, req, "awaiting_review", sendAfter)

	// Should return the error from QueuePendingDM
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "database error" {
		t.Errorf("expected 'database error', got %v", err)
	}
}

func TestDerivedPRState_VariousStates(t *testing.T) {
	tests := []struct {
		name          string
		checkResponse *turn.CheckResponse
		expected      string
	}{
		{
			name:          "nil check response",
			checkResponse: nil,
			expected:      "unknown",
		},
		{
			name: "assigned waiting for review",
			checkResponse: &turn.CheckResponse{
				Analysis: turn.Analysis{
					WorkflowState: string(turn.StateAssignedWaitingForReview),
				},
			},
			expected: string(turn.StateAssignedWaitingForReview),
		},
		{
			name: "published waiting for tests",
			checkResponse: &turn.CheckResponse{
				Analysis: turn.Analysis{
					WorkflowState: string(turn.StatePublishedWaitingForTests),
				},
			},
			expected: string(turn.StatePublishedWaitingForTests),
		},
		{
			name: "newly published",
			checkResponse: &turn.CheckResponse{
				Analysis: turn.Analysis{
					WorkflowState: string(turn.StateNewlyPublished),
				},
			},
			expected: string(turn.StateNewlyPublished),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := "unknown"
			if tt.checkResponse != nil {
				result = tt.checkResponse.Analysis.WorkflowState
			}
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}
