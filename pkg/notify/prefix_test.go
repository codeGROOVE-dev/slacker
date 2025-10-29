package notify

import (
	"testing"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestPrefixForAnalysis(t *testing.T) {
	tests := []struct {
		name          string
		workflowState string
		nextAction    map[string]turn.Action
		expectedEmoji string
	}{
		{
			name:          "newly published always shows :new:",
			workflowState: "newly_published",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionFixTests},
			},
			expectedEmoji: ":new:",
		},
		{
			name:          "publish_draft shows :construction:",
			workflowState: "awaiting_action",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionPublishDraft},
			},
			expectedEmoji: ":construction:",
		},
		{
			name:          "fix_tests shows :cockroach:",
			workflowState: "blocked",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionFixTests},
			},
			expectedEmoji: ":cockroach:",
		},
		{
			name:          "tests_pending shows :test_tube:",
			workflowState: "blocked",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionTestsPending},
			},
			expectedEmoji: ":test_tube:",
		},
		{
			name:          "review shows :hourglass:",
			workflowState: "awaiting_review",
			nextAction: map[string]turn.Action{
				"reviewer": {Kind: turn.ActionReview},
			},
			expectedEmoji: ":hourglass:",
		},
		{
			name:          "resolve_comments shows :carpentry_saw:",
			workflowState: "changes_requested",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionResolveComments},
			},
			expectedEmoji: ":carpentry_saw:",
		},
		{
			name:          "approve shows :white_check_mark:",
			workflowState: "approved",
			nextAction: map[string]turn.Action{
				"maintainer": {Kind: turn.ActionApprove},
			},
			expectedEmoji: ":white_check_mark:",
		},
		{
			name:          "merge shows :rocket:",
			workflowState: "approved",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionMerge},
			},
			expectedEmoji: ":rocket:",
		},
		{
			name:          "publish_draft has highest priority over fix_tests",
			workflowState: "blocked",
			nextAction: map[string]turn.Action{
				"author":   {Kind: turn.ActionPublishDraft},
				"reviewer": {Kind: turn.ActionFixTests},
			},
			expectedEmoji: ":construction:",
		},
		{
			name:          "fix_tests has higher priority than review",
			workflowState: "blocked",
			nextAction: map[string]turn.Action{
				"author":   {Kind: turn.ActionFixTests},
				"reviewer": {Kind: turn.ActionReview},
			},
			expectedEmoji: ":cockroach:",
		},
		{
			name:          "empty next_action shows fallback",
			workflowState: "unknown",
			nextAction:    map[string]turn.Action{},
			expectedEmoji: ":postal_horn:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PrefixForAnalysis(tt.workflowState, tt.nextAction)
			if result != tt.expectedEmoji {
				t.Errorf("expected %s, got %s", tt.expectedEmoji, result)
			}
		})
	}
}

func TestPrimaryAction(t *testing.T) {
	tests := []struct {
		name           string
		nextAction     map[string]turn.Action
		expectedAction string
	}{
		{
			name: "single action",
			nextAction: map[string]turn.Action{
				"user1": {Kind: turn.ActionReview},
			},
			expectedAction: "review",
		},
		{
			name: "publish_draft beats everything",
			nextAction: map[string]turn.Action{
				"user1": {Kind: turn.ActionPublishDraft},
				"user2": {Kind: turn.ActionFixTests},
				"user3": {Kind: turn.ActionReview},
			},
			expectedAction: "publish_draft",
		},
		{
			name: "fix_tests beats review",
			nextAction: map[string]turn.Action{
				"user1": {Kind: turn.ActionFixTests},
				"user2": {Kind: turn.ActionReview},
				"user3": {Kind: turn.ActionApprove},
			},
			expectedAction: "fix_tests",
		},
		{
			name: "tests_pending beats resolve_comments",
			nextAction: map[string]turn.Action{
				"user1": {Kind: turn.ActionTestsPending},
				"user2": {Kind: turn.ActionResolveComments},
			},
			expectedAction: "tests_pending",
		},
		{
			name:           "empty returns empty",
			nextAction:     map[string]turn.Action{},
			expectedAction: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PrimaryAction(tt.nextAction)
			if result != tt.expectedAction {
				t.Errorf("expected %s, got %s", tt.expectedAction, result)
			}
		})
	}
}

func TestActionPriority(t *testing.T) {
	// Verify priority ordering
	priorities := []struct {
		action   string
		priority int
	}{
		{"publish_draft", 1},
		{"fix_tests", 2},
		{"tests_pending", 3},
		{"fix_conflict", 4},
		{"resolve_comments", 5},
		{"review", 6},
		{"approve", 7},
		{"merge", 8},
		{"unknown_action", 99},
	}

	for _, tt := range priorities {
		t.Run(tt.action, func(t *testing.T) {
			result := actionPriority(tt.action)
			if result != tt.priority {
				t.Errorf("expected priority %d for %s, got %d", tt.priority, tt.action, result)
			}
		})
	}
}
