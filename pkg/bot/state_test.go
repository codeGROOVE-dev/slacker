package bot

import (
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestExtractStateFromTurnclient(t *testing.T) {
	c := &Coordinator{}

	tests := []struct {
		name          string
		checkResult   *turn.CheckResponse
		expectedState string
	}{
		{
			name: "merged PR",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State:  "closed",
					Merged: true,
				},
			},
			expectedState: "merged",
		},
		{
			name: "closed but not merged",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State:  "closed",
					Merged: false,
				},
			},
			expectedState: "closed",
		},
		{
			name: "draft PR",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: true,
				},
			},
			expectedState: "tests_running",
		},
		{
			name: "failing checks",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: false,
				},
				Analysis: turn.Analysis{
					Checks: turn.Checks{
						Failing: 3,
						Passing: 10,
					},
				},
			},
			expectedState: "tests_broken",
		},
		{
			name: "pending checks",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: false,
				},
				Analysis: turn.Analysis{
					Checks: turn.Checks{
						Pending: 2,
						Passing: 8,
					},
				},
			},
			expectedState: "tests_running",
		},
		{
			name: "waiting checks",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: false,
				},
				Analysis: turn.Analysis{
					Checks: turn.Checks{
						Waiting: 1,
						Passing: 9,
					},
				},
			},
			expectedState: "tests_running",
		},
		{
			name: "approved with unresolved comments",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: false,
				},
				Analysis: turn.Analysis{
					Approved:           true,
					UnresolvedComments: 2,
					Checks: turn.Checks{
						Passing: 10,
					},
				},
			},
			expectedState: "changes_requested",
		},
		{
			name: "approved and ready",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: false,
				},
				Analysis: turn.Analysis{
					Approved:           true,
					UnresolvedComments: 0,
					Checks: turn.Checks{
						Passing: 10,
					},
				},
			},
			expectedState: "approved",
		},
		{
			name: "awaiting review (default)",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: false,
				},
				Analysis: turn.Analysis{
					Approved: false,
					Checks: turn.Checks{
						Passing: 10,
					},
				},
			},
			expectedState: "awaiting_review",
		},
		{
			name: "all checks passing but not approved",
			checkResult: &turn.CheckResponse{
				PullRequest: prx.PullRequest{
					State: "open",
					Draft: false,
				},
				Analysis: turn.Analysis{
					Approved: false,
					Checks: turn.Checks{
						Passing: 15,
						Failing: 0,
						Pending: 0,
						Waiting: 0,
					},
				},
			},
			expectedState: "awaiting_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.extractStateFromTurnclient(tt.checkResult)
			if result != tt.expectedState {
				t.Errorf("extractStateFromTurnclient() = %q, want %q", result, tt.expectedState)
			}
		})
	}
}

func TestGetStateQueryParam(t *testing.T) {
	c := &Coordinator{}

	tests := []struct {
		name     string
		prState  string
		expected string
	}{
		{
			name:     "tests running",
			prState:  "tests_running",
			expected: "?st=tests_running",
		},
		{
			name:     "tests broken",
			prState:  "tests_broken",
			expected: "?st=tests_broken",
		},
		{
			name:     "awaiting review",
			prState:  "awaiting_review",
			expected: "?st=awaiting_review",
		},
		{
			name:     "changes requested",
			prState:  "changes_requested",
			expected: "?st=changes_requested",
		},
		{
			name:     "approved",
			prState:  "approved",
			expected: "?st=approved",
		},
		{
			name:     "merged (no suffix)",
			prState:  "merged",
			expected: "",
		},
		{
			name:     "closed (no suffix)",
			prState:  "closed",
			expected: "",
		},
		{
			name:     "unknown state (no suffix)",
			prState:  "unknown",
			expected: "",
		},
		{
			name:     "empty state",
			prState:  "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.getStateQueryParam(tt.prState)
			if result != tt.expected {
				t.Errorf("getStateQueryParam(%q) = %q, want %q", tt.prState, result, tt.expected)
			}
		})
	}
}

func TestThreadCache_GetSet(t *testing.T) {
	threadCache := cache.New()

	prKey := "owner/repo#123:C123"

	// Test Get on empty cache
	_, exists := threadCache.Get(prKey)
	if exists {
		t.Error("expected Get to return false for non-existent key")
	}

	// Test Set
	testInfo := ThreadInfo{
		ThreadTS:    "1234567890.123456",
		MessageText: "Test PR message",
		ChannelID:   "C123",
		// UpdatedAt will be set by Set()
	}

	threadCache.Set(prKey, testInfo)

	// Test Get after Set
	info, exists := threadCache.Get(prKey)
	if !exists {
		t.Fatal("expected Get to return true after Set")
	}

	if info.ThreadTS != testInfo.ThreadTS {
		t.Errorf("expected ThreadTS %q, got %q", testInfo.ThreadTS, info.ThreadTS)
	}

	if info.MessageText != testInfo.MessageText {
		t.Errorf("expected MessageText %q, got %q", testInfo.MessageText, info.MessageText)
	}

	if info.ChannelID != testInfo.ChannelID {
		t.Errorf("expected ChannelID %q, got %q", testInfo.ChannelID, info.ChannelID)
	}

	// Verify UpdatedAt was set
	if info.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set by Set()")
	}

	// Verify it's recent
	if time.Since(info.UpdatedAt) > time.Second {
		t.Errorf("expected recent UpdatedAt, got %v", info.UpdatedAt)
	}
}

func TestThreadCache_Cleanup(t *testing.T) {
	threadCache := cache.New()

	now := time.Now()

	// Add old entry (2 hours ago)
	oldInfo := ThreadInfo{
		ThreadTS:    "1111111111.111111",
		MessageText: "Old PR",
		ChannelID:   "C111",
		UpdatedAt:   now.Add(-2 * time.Hour),
	}
	threadCache.SetForTest("old/repo#1:C111", oldInfo)

	// Add recent entry (30 minutes ago)
	recentInfo := ThreadInfo{
		ThreadTS:    "2222222222.222222",
		MessageText: "Recent PR",
		ChannelID:   "C222",
		UpdatedAt:   now.Add(-30 * time.Minute),
	}
	threadCache.SetForTest("recent/repo#2:C222", recentInfo)

	// Add very recent entry (5 minutes ago)
	veryRecentInfo := ThreadInfo{
		ThreadTS:    "3333333333.333333",
		MessageText: "New PR",
		ChannelID:   "C333",
		UpdatedAt:   now.Add(-5 * time.Minute),
	}
	threadCache.SetForTest("new/repo#3:C333", veryRecentInfo)

	// Cleanup entries older than 1 hour
	threadCache.Cleanup(1 * time.Hour)

	// Verify old entry was removed
	_, exists := threadCache.Get("old/repo#1:C111")
	if exists {
		t.Error("expected old entry to be removed by Cleanup")
	}

	// Verify recent entries were kept
	_, exists = threadCache.Get("recent/repo#2:C222")
	if !exists {
		t.Error("expected recent entry to be kept by Cleanup")
	}

	_, exists = threadCache.Get("new/repo#3:C333")
	if !exists {
		t.Error("expected very recent entry to be kept by Cleanup")
	}
}

func TestThreadCache_MultipleChannels(t *testing.T) {
	threadCache := cache.New()

	// Same PR posted to multiple channels
	prKey1 := "owner/repo#123:C111"
	prKey2 := "owner/repo#123:C222"

	info1 := ThreadInfo{
		ThreadTS:    "1111111111.111111",
		MessageText: "PR in channel 1",
		ChannelID:   "C111",
	}

	info2 := ThreadInfo{
		ThreadTS:    "2222222222.222222",
		MessageText: "PR in channel 2",
		ChannelID:   "C222",
	}

	threadCache.Set(prKey1, info1)
	threadCache.Set(prKey2, info2)

	// Verify both are stored independently
	retrievedInfo1, exists1 := threadCache.Get(prKey1)
	retrievedInfo2, exists2 := threadCache.Get(prKey2)

	if !exists1 || !exists2 {
		t.Fatal("expected both channel entries to exist")
	}

	if retrievedInfo1.ChannelID != "C111" {
		t.Errorf("expected ChannelID C111, got %q", retrievedInfo1.ChannelID)
	}

	if retrievedInfo2.ChannelID != "C222" {
		t.Errorf("expected ChannelID C222, got %q", retrievedInfo2.ChannelID)
	}

	if retrievedInfo1.ThreadTS == retrievedInfo2.ThreadTS {
		t.Error("expected different ThreadTS for different channels")
	}
}

func TestThreadCache_UpdateExisting(t *testing.T) {
	threadCache := cache.New()

	prKey := "owner/repo#123:C123"

	// Set initial info
	initialInfo := ThreadInfo{
		ThreadTS:    "1111111111.111111",
		MessageText: "Initial message",
		ChannelID:   "C123",
	}
	threadCache.Set(prKey, initialInfo)

	// Get initial UpdatedAt
	info1, _ := threadCache.Get(prKey)
	firstUpdatedAt := info1.UpdatedAt

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Update with new message text
	updatedInfo := ThreadInfo{
		ThreadTS:    "1111111111.111111", // Same thread
		MessageText: "Updated message",   // New message text
		ChannelID:   "C123",
	}
	threadCache.Set(prKey, updatedInfo)

	// Verify update
	info2, exists := threadCache.Get(prKey)
	if !exists {
		t.Fatal("expected updated info to exist")
	}

	if info2.MessageText != "Updated message" {
		t.Errorf("expected updated MessageText, got %q", info2.MessageText)
	}

	// Verify UpdatedAt was refreshed
	if !info2.UpdatedAt.After(firstUpdatedAt) {
		t.Error("expected UpdatedAt to be refreshed on update")
	}
}
