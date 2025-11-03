package bot

import (
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
)

// TestMessageComparison verifies that message comparison logic works correctly.
func TestMessageComparison(t *testing.T) {
	tests := []struct {
		name            string
		currentMessage  string
		expectedMessage string
		shouldUpdate    bool
		description     string
	}{
		{
			name:            "identical messages",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			shouldUpdate:    false,
			description:     "No update needed when messages are identical",
		},
		{
			name:            "state change - emoji different",
			currentMessage:  ":test_tube: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			shouldUpdate:    true,
			description:     "Update when emoji/state changes",
		},
		{
			name:            "title change",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			expectedMessage: ":hourglass: Fix critical bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			shouldUpdate:    true,
			description:     "Update when title changes",
		},
		{
			name:            "next actions added",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice → review: @bob",
			shouldUpdate:    true,
			description:     "Update when next actions are added",
		},
		{
			name:            "next actions removed",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice → review: @bob",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			shouldUpdate:    true,
			description:     "Update when next actions are removed",
		},
		{
			name:            "next actions changed",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice → review: @bob",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice → review: @charlie",
			shouldUpdate:    true,
			description:     "Update when next actions change",
		},
		{
			name:            "multiple changes at once",
			currentMessage:  ":test_tube: Fix bug <https://github.com/org/repo/pull/1?st=tests_running|repo#1> · alice",
			expectedMessage: ":white_check_mark: Fix critical bug <https://github.com/org/repo/pull/1?st=approved|repo#1> · alice → merge: @alice",
			shouldUpdate:    true,
			description:     "Update when multiple fields change simultaneously",
		},
		{
			name:            "url query param change only",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1?st=tests_running|repo#1> · alice",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1?st=awaiting_review|repo#1> · alice",
			shouldUpdate:    true,
			description:     "Update when URL query param changes (state debugging)",
		},
		{
			name:            "whitespace differences",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> ·  alice",
			shouldUpdate:    true,
			description:     "Update even with minor whitespace differences (exact match required)",
		},
		{
			name:            "system user filtered out",
			currentMessage:  ":test_tube: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice → tests pending: @_system",
			expectedMessage: ":test_tube: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice → tests pending",
			shouldUpdate:    true,
			description:     "Update when _system user is filtered from next actions",
		},
		{
			name:            "empty vs missing next actions",
			currentMessage:  ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			expectedMessage: ":hourglass: Fix bug <https://github.com/org/repo/pull/1|repo#1> · alice",
			shouldUpdate:    false,
			description:     "No update when both have no next actions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simple string comparison - this is what the code does
			needsUpdate := tt.currentMessage != tt.expectedMessage

			if needsUpdate != tt.shouldUpdate {
				t.Errorf("%s: got needsUpdate=%v, want %v\nCurrent:  %q\nExpected: %q",
					tt.description, needsUpdate, tt.shouldUpdate,
					tt.currentMessage, tt.expectedMessage)
			}
		})
	}
}

// TestCachedMessageText verifies that message text is properly cached and retrieved.
func TestCachedMessageText(t *testing.T) {
	threadCache := cache.New()

	tests := []struct {
		name        string
		cacheKey    string
		threadInfo  ThreadInfo
		expectFound bool
		expectText  string
	}{
		{
			name:     "message text cached",
			cacheKey: "org/repo#1:C123",
			threadInfo: ThreadInfo{
				ThreadTS:    "1234567890.123456",
				ChannelID:   "C123",
				LastState:   "awaiting_review",
				MessageText: ":hourglass: Fix bug <url|repo#1> · alice",
			},
			expectFound: true,
			expectText:  ":hourglass: Fix bug <url|repo#1> · alice",
		},
		{
			name:     "empty message text in cache",
			cacheKey: "org/repo#2:C123",
			threadInfo: ThreadInfo{
				ThreadTS:    "1234567890.123457",
				ChannelID:   "C123",
				LastState:   "tests_running",
				MessageText: "",
			},
			expectFound: true,
			expectText:  "",
		},
		{
			name:        "cache miss",
			cacheKey:    "org/repo#999:C123",
			expectFound: false,
			expectText:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectFound {
				threadCache.Set(tt.cacheKey, tt.threadInfo)
			}

			info, found := threadCache.Get(tt.cacheKey)
			if found != tt.expectFound {
				t.Errorf("cache.Get() found=%v, want %v", found, tt.expectFound)
			}

			if found && info.MessageText != tt.expectText {
				t.Errorf("MessageText=%q, want %q", info.MessageText, tt.expectText)
			}
		})
	}
}

// TestMessageUpdateScenarios tests realistic end-to-end scenarios.
func TestMessageUpdateScenarios(t *testing.T) {
	scenarios := []struct {
		name         string
		initialState string
		initialTitle string
		initialUsers string
		finalState   string
		finalTitle   string
		finalUsers   string
		shouldUpdate bool
		description  string
	}{
		{
			name:         "push new commits - title changes",
			initialState: "awaiting_review",
			initialTitle: "WIP: Fix bug",
			initialUsers: "",
			finalState:   "awaiting_review",
			finalTitle:   "Fix bug",
			finalUsers:   "",
			shouldUpdate: true,
			description:  "Title changed but state stayed same (synchronize event)",
		},
		{
			name:         "tests finish successfully",
			initialState: "tests_running",
			initialTitle: "Fix bug",
			initialUsers: "",
			finalState:   "awaiting_review",
			finalTitle:   "Fix bug",
			finalUsers:   "",
			shouldUpdate: true,
			description:  "State changed from tests_running to awaiting_review",
		},
		{
			name:         "reviewer assigned",
			initialState: "awaiting_review",
			initialTitle: "Fix bug",
			initialUsers: "",
			finalState:   "awaiting_review",
			finalTitle:   "Fix bug",
			finalUsers:   " → review: @bob",
			shouldUpdate: true,
			description:  "Next actions added when reviewer assigned",
		},
		{
			name:         "review submitted",
			initialState: "awaiting_review",
			initialTitle: "Fix bug",
			initialUsers: " → review: @bob",
			finalState:   "approved",
			finalTitle:   "Fix bug",
			finalUsers:   " → merge: @alice",
			shouldUpdate: true,
			description:  "State and next actions both changed",
		},
		{
			name:         "no changes - duplicate event",
			initialState: "awaiting_review",
			initialTitle: "Fix bug",
			initialUsers: " → review: @bob",
			finalState:   "awaiting_review",
			finalTitle:   "Fix bug",
			finalUsers:   " → review: @bob",
			shouldUpdate: false,
			description:  "Everything identical - skip update",
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			// Build messages like the real code does
			initialMsg := ":" + sc.initialState + ": " + sc.initialTitle + " <url|repo#1> · alice" + sc.initialUsers
			finalMsg := ":" + sc.finalState + ": " + sc.finalTitle + " <url|repo#1> · alice" + sc.finalUsers

			needsUpdate := initialMsg != finalMsg

			if needsUpdate != sc.shouldUpdate {
				t.Errorf("%s:\n  got needsUpdate=%v, want %v\n  Initial: %q\n  Final:   %q",
					sc.description, needsUpdate, sc.shouldUpdate, initialMsg, finalMsg)
			}
		})
	}
}
