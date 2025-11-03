package bot

import (
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestExtractStateFromTurnclient_Merged tests merged PR state detection.
func TestExtractStateFromTurnclient_Merged(t *testing.T) {
	c := &Coordinator{}

	now := time.Now()
	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:    "closed",
			Merged:   true,
			MergedAt: &now,
		},
		Analysis: turn.Analysis{},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "merged" {
		t.Errorf("expected 'merged', got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_ClosedNotMerged tests closed-but-not-merged state.
func TestExtractStateFromTurnclient_ClosedNotMerged(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State:  "closed",
			Merged: false,
		},
		Analysis: turn.Analysis{},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "closed" {
		t.Errorf("expected 'closed', got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_Draft tests draft PR state detection.
func TestExtractStateFromTurnclient_Draft(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
			Draft: true,
		},
		Analysis: turn.Analysis{},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "tests_running" {
		t.Errorf("expected 'tests_running' for draft PR, got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_TestsBroken tests failing checks state.
func TestExtractStateFromTurnclient_TestsBroken(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
		},
		Analysis: turn.Analysis{
			Checks: turn.Checks{
				Failing: 3,
				Passing: 2,
			},
		},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "tests_broken" {
		t.Errorf("expected 'tests_broken', got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_TestsRunning tests pending/waiting checks state.
func TestExtractStateFromTurnclient_TestsRunning(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
		},
		Analysis: turn.Analysis{
			Checks: turn.Checks{
				Pending: 2,
				Passing: 3,
			},
		},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "tests_running" {
		t.Errorf("expected 'tests_running', got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_TestsRunningWithWaiting tests waiting checks state.
func TestExtractStateFromTurnclient_TestsRunningWithWaiting(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
		},
		Analysis: turn.Analysis{
			Checks: turn.Checks{
				Waiting: 1,
				Passing: 3,
			},
		},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "tests_running" {
		t.Errorf("expected 'tests_running', got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_Approved tests approved PR state.
func TestExtractStateFromTurnclient_Approved(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
		},
		Analysis: turn.Analysis{
			Approved: true,
			Checks: turn.Checks{
				Passing: 5,
			},
			UnresolvedComments: 0,
		},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "approved" {
		t.Errorf("expected 'approved', got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_ChangesRequested tests approved with unresolved comments.
func TestExtractStateFromTurnclient_ChangesRequested(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
		},
		Analysis: turn.Analysis{
			Approved: true,
			Checks: turn.Checks{
				Passing: 5,
			},
			UnresolvedComments: 3,
		},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "changes_requested" {
		t.Errorf("expected 'changes_requested', got '%s'", state)
	}
}

// TestExtractStateFromTurnclient_AwaitingReview tests default awaiting review state.
func TestExtractStateFromTurnclient_AwaitingReview(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{
			State: "open",
		},
		Analysis: turn.Analysis{
			Approved: false,
			Checks: turn.Checks{
				Passing: 5,
			},
			UnresolvedComments: 0,
		},
	}

	state := c.extractStateFromTurnclient(checkResult)
	if state != "awaiting_review" {
		t.Errorf("expected 'awaiting_review', got '%s'", state)
	}
}

// TestExtractBlockedUsersFromTurnclient_WithUsers tests extraction of blocked users.
func TestExtractBlockedUsersFromTurnclient_WithUsers(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"alice": {Kind: "review"},
				"bob":   {Kind: "approve"},
				"carol": {Kind: "merge"},
			},
		},
	}

	blockedUsers := c.extractBlockedUsersFromTurnclient(checkResult)

	if len(blockedUsers) != 3 {
		t.Errorf("expected 3 blocked users, got %d", len(blockedUsers))
	}

	// Convert to map for easier checking
	usersMap := make(map[string]bool)
	for _, user := range blockedUsers {
		usersMap[user] = true
	}

	expectedUsers := []string{"alice", "bob", "carol"}
	for _, expected := range expectedUsers {
		if !usersMap[expected] {
			t.Errorf("expected user '%s' in blocked users", expected)
		}
	}
}

// TestExtractBlockedUsersFromTurnclient_WithSystemUser tests filtering of _system sentinel.
func TestExtractBlockedUsersFromTurnclient_WithSystemUser(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"alice":   {Kind: "review"},
				"_system": {Kind: "processing"},
				"bob":     {Kind: "approve"},
			},
		},
	}

	blockedUsers := c.extractBlockedUsersFromTurnclient(checkResult)

	if len(blockedUsers) != 2 {
		t.Errorf("expected 2 blocked users (excluding _system), got %d", len(blockedUsers))
	}

	// Ensure _system is not included
	for _, user := range blockedUsers {
		if user == "_system" {
			t.Error("_system should be filtered out from blocked users")
		}
	}
}

// TestExtractBlockedUsersFromTurnclient_Empty tests empty NextAction map.
func TestExtractBlockedUsersFromTurnclient_Empty(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{},
		},
	}

	blockedUsers := c.extractBlockedUsersFromTurnclient(checkResult)

	if len(blockedUsers) != 0 {
		t.Errorf("expected 0 blocked users for empty NextAction, got %d", len(blockedUsers))
	}
}

// TestExtractBlockedUsersFromTurnclient_OnlySystem tests NextAction with only _system.
func TestExtractBlockedUsersFromTurnclient_OnlySystem(t *testing.T) {
	c := &Coordinator{}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"_system": {Kind: "processing"},
			},
		},
	}

	blockedUsers := c.extractBlockedUsersFromTurnclient(checkResult)

	if len(blockedUsers) != 0 {
		t.Errorf("expected 0 blocked users when only _system present, got %d", len(blockedUsers))
	}
}
