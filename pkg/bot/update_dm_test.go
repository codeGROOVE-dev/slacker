package bot

import (
	"context"
	"testing"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestUpdateDMMessagesForPR_MergedPR tests updating DMs for a merged PR.
func TestUpdateDMMessagesForPR_MergedPR(t *testing.T) {
	ctx := context.Background()

	mockStore := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers: map[string][]string{
			"https://github.com/testorg/testrepo/pull/42": {"U001", "U002"},
		},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    mockStore,
		configManager: config.New(),
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
	}

	pr := prUpdateInfo{
		owner:  "testorg",
		repo:   "testrepo",
		number: 42,
		state:  "merged",
		url:    "https://github.com/testorg/testrepo/pull/42",
		title:  "Test PR",
		author: "testauthor",
		checkRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				WorkflowState: "merged",
			},
		},
	}

	c.updateDMMessagesForPR(ctx, pr)

	// Test passes if it completes without panicking
}

// TestUpdateDMMessagesForPR_ClosedPR tests updating DMs for a closed (but not merged) PR.
func TestUpdateDMMessagesForPR_ClosedPR(t *testing.T) {
	ctx := context.Background()

	mockStore := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers: map[string][]string{
			"https://github.com/testorg/testrepo/pull/42": {"U001"},
		},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    mockStore,
		configManager: config.New(),
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
	}

	pr := prUpdateInfo{
		owner:  "testorg",
		repo:   "testrepo",
		number: 42,
		state:  "closed",
		url:    "https://github.com/testorg/testrepo/pull/42",
		title:  "Test PR",
		author: "testauthor",
		checkRes: &turn.CheckResponse{
			PullRequest: prx.PullRequest{
				State:  "closed",
				Merged: false,
			},
			Analysis: turn.Analysis{
				WorkflowState: "closed",
			},
		},
	}

	c.updateDMMessagesForPR(ctx, pr)

	// Test passes if it completes without panicking
}

// TestUpdateDMMessagesForPR_NoDMRecipients tests when no one received DMs.
func TestUpdateDMMessagesForPR_NoDMRecipients(t *testing.T) {
	ctx := context.Background()

	mockStore := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers:         map[string][]string{}, // Empty - no DM recipients
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    mockStore,
		configManager: config.New(),
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
	}

	pr := prUpdateInfo{
		owner:  "testorg",
		repo:   "testrepo",
		number: 42,
		state:  "merged",
		url:    "https://github.com/testorg/testrepo/pull/42",
		title:  "Test PR",
		author: "testauthor",
	}

	c.updateDMMessagesForPR(ctx, pr)

	// Should return early without errors
}

// TestUpdateDMMessagesForPR_BlockedUsersState tests updating DMs for blocked users.
func TestUpdateDMMessagesForPR_BlockedUsersState(t *testing.T) {
	ctx := context.Background()

	mockStore := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers:         map[string][]string{},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    mockStore,
		configManager: config.New(),
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
	}

	pr := prUpdateInfo{
		owner:  "testorg",
		repo:   "testrepo",
		number: 42,
		state:  "awaiting_review",
		url:    "https://github.com/testorg/testrepo/pull/42",
		title:  "Test PR",
		author: "testauthor",
		checkRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				WorkflowState: "awaiting_review",
				NextAction: map[string]turn.Action{
					"alice": {Kind: "review"},
					"bob":   {Kind: "review"},
				},
			},
		},
	}

	c.updateDMMessagesForPR(ctx, pr)

	// Test passes if it completes without panicking
}

// TestUpdateDMMessagesForPR_NoBlockedUsers tests when no users are blocked.
func TestUpdateDMMessagesForPR_NoBlockedUsers(t *testing.T) {
	ctx := context.Background()

	mockStore := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers:         map[string][]string{},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    mockStore,
		configManager: config.New(),
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
	}

	pr := prUpdateInfo{
		owner:  "testorg",
		repo:   "testrepo",
		number: 42,
		state:  "awaiting_review",
		url:    "https://github.com/testorg/testrepo/pull/42",
		title:  "Test PR",
		author: "testauthor",
		checkRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				WorkflowState: "awaiting_review",
				NextAction:    map[string]turn.Action{}, // No blocked users
			},
		},
	}

	c.updateDMMessagesForPR(ctx, pr)

	// Should return early without errors
}

// TestUpdateDMMessagesForPR_SystemUserOnly tests when only _system is blocked.
func TestUpdateDMMessagesForPR_SystemUserOnly(t *testing.T) {
	ctx := context.Background()

	mockStore := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers:         map[string][]string{},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    mockStore,
		configManager: config.New(),
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
	}

	pr := prUpdateInfo{
		owner:  "testorg",
		repo:   "testrepo",
		number: 42,
		state:  "tests_broken",
		url:    "https://github.com/testorg/testrepo/pull/42",
		title:  "Test PR",
		author: "testauthor",
		checkRes: &turn.CheckResponse{
			Analysis: turn.Analysis{
				WorkflowState: "tests_broken",
				NextAction: map[string]turn.Action{
					"_system": {Kind: "fix_tests"}, // Only system user
				},
			},
		},
	}

	c.updateDMMessagesForPR(ctx, pr)

	// Should return early after filtering out _system
}

// TestUpdateDMMessagesForPR_NilCheckResult tests when checkResult is nil.
func TestUpdateDMMessagesForPR_NilCheckResult(t *testing.T) {
	ctx := context.Background()

	mockStore := &mockStateStore{
		processedEvents: make(map[string]bool),
		dmUsers: map[string][]string{
			"https://github.com/testorg/testrepo/pull/42": {"U001"},
		},
	}

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    mockStore,
		configManager: config.New(),
		userMapper:    &mockUserMapper{},
		threadCache:   cache.New(),
	}

	pr := prUpdateInfo{
		owner:    "testorg",
		repo:     "testrepo",
		number:   42,
		state:    "merged",
		url:      "https://github.com/testorg/testrepo/pull/42",
		title:    "Test PR",
		author:   "testauthor",
		checkRes: nil, // No check result
	}

	c.updateDMMessagesForPR(ctx, pr)

	// Should handle nil checkResult gracefully using state-based fallback
}
