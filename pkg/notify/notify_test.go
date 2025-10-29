package notify

import (
	"context"
	"testing"
	"time"
)

// TestNotifyUserRequiresDeeperMocking documents that NotifyUser needs slack.Client interface.
// Current mock only covers SlackManager.Client() but not slack.Client methods like
// IsUserActive(), IsUserInChannel(), SendDirectMessage().
func TestNotifyUserRequiresDeeperMocking(t *testing.T) {
	t.Skip("NotifyUser testing requires slack.Client interface extraction for: IsUserActive, IsUserInChannel, SendDirectMessage")
}

// TestNotifyManagerRun tests the notification scheduler Run method.
func TestNotifyManagerRun(t *testing.T) {
	mockSlackMgr := &mockSlackManager{}
	mockConfigMgr := &mockConfigManager{}

	manager := New(mockSlackMgr, mockConfigMgr, &mockStore{})

	// Create a context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Run should return when context is cancelled
	err := manager.Run(ctx)
	if err != context.DeadlineExceeded && err != context.Canceled {
		t.Errorf("expected context error, got %v", err)
	}
}

// TestPRInfo tests PRInfo struct usage.
func TestPRInfo(t *testing.T) {
	pr := PRInfo{
		Owner:         "test-org",
		Repo:          "test-repo",
		Title:         "Test PR",
		Author:        "testuser",
		State:         "open",
		HTMLURL:       "https://github.com/test-org/test-repo/pull/123",
		Number:        123,
		WorkflowState: "awaiting_review",
	}

	if pr.Owner != "test-org" {
		t.Errorf("expected Owner %q, got %q", "test-org", pr.Owner)
	}
	if pr.Number != 123 {
		t.Errorf("expected Number %d, got %d", 123, pr.Number)
	}
}

// TestMessageParams tests MessageParams struct.
func TestMessageParams(t *testing.T) {
	params := MessageParams{
		Owner:    "test-org",
		Repo:     "test-repo",
		PRNumber: 123,
		Title:    "Test PR",
		Author:   "testuser",
		HTMLURL:  "https://github.com/test-org/test-repo/pull/123",
		Domain:   "example.com",
	}

	if params.Owner != "test-org" {
		t.Errorf("expected Owner %q, got %q", "test-org", params.Owner)
	}
	if params.PRNumber != 123 {
		t.Errorf("expected PRNumber %d, got %d", 123, params.PRNumber)
	}
}
