package notify

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/internal/config"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestPrefixForState tests state-based emoji selection.
func TestPrefixForState(t *testing.T) {
	tests := []struct {
		state    string
		expected string
	}{
		{"newly_published", ":new:"},
		{"awaiting_review", ":hourglass:"},
		{"tests_broken", ":cockroach:"},
		{"tests_running", ":test_tube:"},
		{"changes_requested", ":carpentry_saw:"},
		{"approved", ":white_check_mark:"},
		{"merged", ":rocket:"},
		{"closed", ":x:"},
		{"unknown_state", ":postal_horn:"},
		{"", ":postal_horn:"},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			emoji := PrefixForState(tt.state)
			if emoji != tt.expected {
				t.Errorf("expected %q for state %q, got %q", tt.expected, tt.state, emoji)
			}
		})
	}
}

// TestPrefixForAction tests action kind string to emoji mapping.
func TestPrefixForAction(t *testing.T) {
	tests := []struct {
		actionKind string
		expected   string
	}{
		{"review", ":hourglass:"},
		{"re_review", ":hourglass:"},
		{"request_reviewers", ":hourglass:"},
		{"merge", ":rocket:"},
		{"approve", ":white_check_mark:"},
		{"fix_tests", ":cockroach:"},
		{"tests_pending", ":test_tube:"},
		{"publish_draft", ":construction:"},
		{"resolve_comments", ":carpentry_saw:"},
		{"respond", ":carpentry_saw:"},
		{"review_discussion", ":carpentry_saw:"},
		{"unknown_action", ":postal_horn:"},
	}

	for _, tt := range tests {
		t.Run(tt.actionKind, func(t *testing.T) {
			emoji := PrefixForAction(tt.actionKind)
			if emoji != tt.expected {
				t.Errorf("expected %q for action %q, got %q", tt.expected, tt.actionKind, emoji)
			}
		})
	}
}

// TestFormatNextActionsSuffix tests the suffix formatting for empty cases.
func TestFormatNextActionsSuffix(t *testing.T) {
	ctx := context.Background()

	// Test with nil CheckResult
	params := MessageParams{
		CheckResult: nil,
	}
	suffix := FormatNextActionsSuffix(ctx, params)
	if suffix != "" {
		t.Errorf("expected empty suffix for nil CheckResult, got %q", suffix)
	}

	// Test with empty NextAction map
	params.CheckResult = &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{},
		},
	}
	suffix = FormatNextActionsSuffix(ctx, params)
	if suffix != "" {
		t.Errorf("expected empty suffix for empty NextAction, got %q", suffix)
	}
}

// TestStateParam tests the URL state parameter generation.
func TestStateParam(t *testing.T) {
	tests := []struct {
		name     string
		response *turn.CheckResponse
		expected string
	}{
		{
			name: "draft PR",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: true},
				Analysis:    turn.Analysis{},
			},
			expected: "?st=tests_running",
		},
		{
			name: "failing checks",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Checks: turn.Checks{Failing: 2}},
			},
			expected: "?st=tests_broken",
		},
		{
			name: "pending checks",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Checks: turn.Checks{Pending: 1}},
			},
			expected: "?st=tests_running",
		},
		{
			name: "waiting checks",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Checks: turn.Checks{Waiting: 1}},
			},
			expected: "?st=tests_running",
		},
		{
			name: "approved with unresolved comments",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Approved: true, UnresolvedComments: 3},
			},
			expected: "?st=changes_requested",
		},
		{
			name: "approved without unresolved comments",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Approved: true, UnresolvedComments: 0},
			},
			expected: "?st=approved",
		},
		{
			name: "awaiting review",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Approved: false},
			},
			expected: "?st=awaiting_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stateParam(tt.response)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestFallbackEmoji tests emoji selection when no workflow_state or next_actions.
func TestFallbackEmoji(t *testing.T) {
	tests := []struct {
		name         string
		response     *turn.CheckResponse
		expectedEmoji string
		expectedState string
	}{
		{
			name: "draft PR",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: true},
				Analysis:    turn.Analysis{},
			},
			expectedEmoji: ":test_tube:",
			expectedState: "?st=tests_running",
		},
		{
			name: "failing checks",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Checks: turn.Checks{Failing: 1}},
			},
			expectedEmoji: ":cockroach:",
			expectedState: "?st=tests_broken",
		},
		{
			name: "pending checks",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Checks: turn.Checks{Pending: 2}},
			},
			expectedEmoji: ":test_tube:",
			expectedState: "?st=tests_running",
		},
		{
			name: "waiting checks",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Checks: turn.Checks{Waiting: 1}},
			},
			expectedEmoji: ":test_tube:",
			expectedState: "?st=tests_running",
		},
		{
			name: "approved with unresolved comments",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Approved: true, UnresolvedComments: 2},
			},
			expectedEmoji: ":carpentry_saw:",
			expectedState: "?st=changes_requested",
		},
		{
			name: "approved",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{Approved: true},
			},
			expectedEmoji: ":white_check_mark:",
			expectedState: "?st=approved",
		},
		{
			name: "awaiting review",
			response: &turn.CheckResponse{
				PullRequest: prx.PullRequest{Draft: false},
				Analysis:    turn.Analysis{},
			},
			expectedEmoji: ":hourglass:",
			expectedState: "?st=awaiting_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			emoji, state := fallbackEmoji(tt.response)
			if emoji != tt.expectedEmoji {
				t.Errorf("expected emoji %q, got %q", tt.expectedEmoji, emoji)
			}
			if state != tt.expectedState {
				t.Errorf("expected state %q, got %q", tt.expectedState, state)
			}
		})
	}
}

// mockUserMapper for testing formatNextActionsInternal
type mockUserMapper struct{}

func (m *mockUserMapper) FormatUserMentions(ctx context.Context, githubUsers []string, owner, domain string) string {
	if len(githubUsers) == 0 {
		return ""
	}
	// Simple mock: just return comma-separated github usernames
	result := ""
	for i, user := range githubUsers {
		if i > 0 {
			result += ", "
		}
		result += "@" + user
	}
	return result
}

// TestFormatNextActionsInternal tests the internal next actions formatter.
func TestFormatNextActionsInternal(t *testing.T) {
	ctx := context.Background()
	mapper := &mockUserMapper{}

	tests := []struct {
		name        string
		nextActions map[string]turn.Action
		expected    string
	}{
		{
			name:        "empty actions",
			nextActions: map[string]turn.Action{},
			expected:    "",
		},
		{
			name: "single action with user",
			nextActions: map[string]turn.Action{
				"user1": {Kind: turn.ActionReview},
			},
			expected: "review: @user1",
		},
		{
			name: "multiple users same action",
			nextActions: map[string]turn.Action{
				"user1": {Kind: turn.ActionReview},
				"user2": {Kind: turn.ActionReview},
			},
			expected: "review: @user1, @user2",
		},
		{
			name: "system user filtered out",
			nextActions: map[string]turn.Action{
				"_system": {Kind: turn.ActionReview},
			},
			expected: "review",
		},
		{
			name: "system and real user",
			nextActions: map[string]turn.Action{
				"_system": {Kind: turn.ActionReview},
				"user1":   {Kind: turn.ActionReview},
			},
			expected: "review: @user1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatNextActionsInternal(ctx, tt.nextActions, "owner", "example.com", mapper)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestFormatChannelMessageBase tests the base channel message formatting.
func TestFormatChannelMessageBase(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		params   MessageParams
		contains []string // Substrings that should be in the result
	}{
		{
			name: "merged PR",
			params: MessageParams{
				CheckResult: &turn.CheckResponse{
					PullRequest: prx.PullRequest{
						Merged:   true,
						MergedAt: timePtr(time.Now()),
					},
					Analysis: turn.Analysis{},
				},
				Owner:    "test-owner",
				Repo:     "test-repo",
				PRNumber: 42,
				Title:    "Test PR",
				Author:   "testuser",
				HTMLURL:  "https://github.com/test-owner/test-repo/pull/42",
			},
			contains: []string{":rocket:", "Test PR", "test-repo#42", "testuser", "?st=merged"},
		},
		{
			name: "closed but not merged",
			params: MessageParams{
				CheckResult: &turn.CheckResponse{
					PullRequest: prx.PullRequest{
						State:  "closed",
						Merged: false,
					},
					Analysis: turn.Analysis{},
				},
				Owner:    "test-owner",
				Repo:     "test-repo",
				PRNumber: 43,
				Title:    "Closed PR",
				Author:   "testuser",
				HTMLURL:  "https://github.com/test-owner/test-repo/pull/43",
			},
			contains: []string{":x:", "Closed PR", "test-repo#43", "testuser", "?st=closed"},
		},
		{
			name: "newly published",
			params: MessageParams{
				CheckResult: &turn.CheckResponse{
					PullRequest: prx.PullRequest{},
					Analysis: turn.Analysis{
						WorkflowState: "newly_published",
					},
				},
				Owner:    "test-owner",
				Repo:     "test-repo",
				PRNumber: 44,
				Title:    "New PR",
				Author:   "testuser",
				HTMLURL:  "https://github.com/test-owner/test-repo/pull/44",
			},
			contains: []string{":new:", "New PR", "test-repo#44", "testuser", "?st=newly_published"},
		},
		{
			name: "with next actions",
			params: MessageParams{
				CheckResult: &turn.CheckResponse{
					PullRequest: prx.PullRequest{},
					Analysis: turn.Analysis{
						NextAction: map[string]turn.Action{
							"user1": {Kind: turn.ActionReview},
						},
					},
				},
				Owner:    "test-owner",
				Repo:     "test-repo",
				PRNumber: 45,
				Title:    "PR needs review",
				Author:   "testuser",
				HTMLURL:  "https://github.com/test-owner/test-repo/pull/45",
			},
			contains: []string{":hourglass:", "PR needs review", "test-repo#45", "testuser"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatChannelMessageBase(ctx, tt.params)
			for _, substr := range tt.contains {
				if !contains(result, substr) {
					t.Errorf("expected result to contain %q, got: %q", substr, result)
				}
			}
		})
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

// indexOf returns the index of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// timePtr returns a pointer to a time.Time value.
func timePtr(t time.Time) *time.Time {
	return &t
}

// TestNew tests the Manager constructor.
func TestNew(t *testing.T) {
	// Create a simple mock config manager
	mockConfig := &mockConfigManager{}

	// Call New - it should not panic
	manager := New(nil, mockConfig)

	if manager == nil {
		t.Fatal("expected non-nil manager")
	}

	if manager.Tracker == nil {
		t.Error("expected Tracker to be initialized")
	}

	if manager.configManager == nil {
		t.Error("expected configManager to be set")
	}
}

// TestNewDailyDigestScheduler tests the DailyDigestScheduler constructor.
func TestNewDailyDigestScheduler(t *testing.T) {
	mockConfig := &mockConfigManager{}
	mockState := &mockStateProvider{}
	mockSlack := &mockSlackManager{}
	manager := New(nil, mockConfig)

	scheduler := NewDailyDigestScheduler(manager, nil, mockConfig, mockState, mockSlack)

	if scheduler == nil {
		t.Fatal("expected non-nil scheduler")
	}

	if scheduler.notifier != manager {
		t.Error("expected notifier to be set")
	}

	if scheduler.configManager == nil {
		t.Error("expected configManager to be set")
	}

	if scheduler.stateStore == nil {
		t.Error("expected stateStore to be set")
	}

	if scheduler.slackManager == nil {
		t.Error("expected slackManager to be set")
	}
}

// mockSlackManager implements SlackManager for testing.
type mockSlackManager struct{}

func (m *mockSlackManager) Client(ctx context.Context, teamID string) (SlackClient, error) {
	return nil, nil
}

// mockConfigManager implements the config interface needed by New and ConfigProvider.
type mockConfigManager struct{}

func (m *mockConfigManager) DailyRemindersEnabled(org string) bool {
	return true
}

func (m *mockConfigManager) ReminderDMDelay(org, channel string) int {
	return 65
}

func (m *mockConfigManager) Domain(org string) string {
	return "example.com"
}

func (m *mockConfigManager) Config(org string) (*config.RepoConfig, bool) {
	return nil, false
}

// TestFormatNextActionsSuffixWithActions tests suffix with actual user actions.
func TestFormatNextActionsSuffixWithActions(t *testing.T) {
	ctx := context.Background()
	mapper := &mockUserMapper{}

	tests := []struct {
		name     string
		params   MessageParams
		expected string
	}{
		{
			name: "suffix with review action",
			params: MessageParams{
				CheckResult: &turn.CheckResponse{
					Analysis: turn.Analysis{
						NextAction: map[string]turn.Action{
							"user1": {Kind: turn.ActionReview},
						},
					},
				},
				Owner:      "test-owner",
				Domain:     "example.com",
				UserMapper: mapper,
			},
			expected: " → review: @user1",
		},
		{
			name: "suffix with multiple users",
			params: MessageParams{
				CheckResult: &turn.CheckResponse{
					Analysis: turn.Analysis{
						NextAction: map[string]turn.Action{
							"user1": {Kind: turn.ActionReview},
							"user2": {Kind: turn.ActionReview},
						},
					},
				},
				Owner:      "test-owner",
				Domain:     "example.com",
				UserMapper: mapper,
			},
			expected: " → review: ",
		},
		{
			name: "suffix filters system user",
			params: MessageParams{
				CheckResult: &turn.CheckResponse{
					Analysis: turn.Analysis{
						NextAction: map[string]turn.Action{
							"_system": {Kind: turn.ActionReview},
						},
					},
				},
				Owner:      "test-owner",
				Domain:     "example.com",
				UserMapper: mapper,
			},
			expected: " → review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatNextActionsSuffix(ctx, tt.params)
			// For tests with multiple users, just check that result starts with the prefix
			// since map iteration order is not guaranteed
			if tt.name == "suffix with multiple users" {
				if !contains(result, " → review: ") {
					t.Errorf("expected result to contain \" → review: \", got: %q", result)
				}
			} else if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
