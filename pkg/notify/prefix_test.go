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
		// Test all WorkflowState constants
		{
			name:          "NEWLY_PUBLISHED shows :new:",
			workflowState: string(turn.StateNewlyPublished),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionRequestReviewers},
			},
			expectedEmoji: ":new:",
		},
		{
			name:          "IN_DRAFT shows :construction:",
			workflowState: string(turn.StateInDraft),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionPublishDraft},
			},
			expectedEmoji: ":construction:",
		},
		{
			name:          "PUBLISHED_WAITING_FOR_TESTS with broken tests shows :cockroach:",
			workflowState: string(turn.StatePublishedWaitingForTests),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionFixTests},
			},
			expectedEmoji: ":cockroach:",
		},
		{
			name:          "PUBLISHED_WAITING_FOR_TESTS with pending tests shows :test_tube:",
			workflowState: string(turn.StatePublishedWaitingForTests),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionTestsPending},
			},
			expectedEmoji: ":test_tube:",
		},
		{
			name:          "PUBLISHED_WAITING_FOR_TESTS defaults to :test_tube:",
			workflowState: string(turn.StatePublishedWaitingForTests),
			nextAction:    map[string]turn.Action{},
			expectedEmoji: ":test_tube:",
		},
		{
			name:          "TESTED_WAITING_FOR_ASSIGNMENT shows :shrug:",
			workflowState: string(turn.StateTestedWaitingForAssignment),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionRequestReviewers},
			},
			expectedEmoji: ":shrug:",
		},
		{
			name:          "TESTED_WAITING_FOR_ASSIGNMENT with broken tests shows :cockroach:",
			workflowState: string(turn.StateTestedWaitingForAssignment),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionFixTests},
			},
			expectedEmoji: ":cockroach:",
		},
		{
			name:          "ASSIGNED_WAITING_FOR_REVIEW shows :hourglass:",
			workflowState: string(turn.StateAssignedWaitingForReview),
			nextAction: map[string]turn.Action{
				"reviewer": {Kind: turn.ActionReview},
			},
			expectedEmoji: ":hourglass:",
		},
		{
			name:          "REVIEWED_NEEDS_REFINEMENT shows :carpentry_saw:",
			workflowState: string(turn.StateReviewedNeedsRefinement),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionResolveComments},
			},
			expectedEmoji: ":carpentry_saw:",
		},
		{
			name:          "REFINED_WAITING_FOR_APPROVAL shows :hourglass:",
			workflowState: string(turn.StateRefinedWaitingForApproval),
			nextAction: map[string]turn.Action{
				"reviewer": {Kind: turn.ActionApprove},
			},
			expectedEmoji: ":hourglass:",
		},
		{
			name:          "APPROVED_WAITING_FOR_MERGE shows :white_check_mark:",
			workflowState: string(turn.StateApprovedWaitingForMerge),
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionMerge},
			},
			expectedEmoji: ":white_check_mark:",
		},
		// Test NextAction fallback when no WorkflowState
		{
			name:          "no workflow_state but publish_draft action shows :construction:",
			workflowState: "",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionPublishDraft},
			},
			expectedEmoji: ":construction:",
		},
		{
			name:          "no workflow_state but fix_tests action shows :cockroach:",
			workflowState: "",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionFixTests},
			},
			expectedEmoji: ":cockroach:",
		},
		{
			name:          "unknown workflow_state falls back to next_action",
			workflowState: "UNKNOWN_STATE",
			nextAction: map[string]turn.Action{
				"author": {Kind: turn.ActionFixTests},
			},
			expectedEmoji: ":cockroach:",
		},
		{
			name:          "no workflow_state and no next_action shows :postal_horn:",
			workflowState: "",
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
