package notify

import (
	"strings"
	"testing"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestFormatDMMessage tests the formatDMMessage function for all PR states.
func TestFormatDMMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		pr             PRInfo
		expectedAction string
		shouldContain  []string
	}{
		{
			name: "newly_published_state",
			pr: PRInfo{
				Owner:         "testorg",
				Repo:          "testrepo",
				Number:        42,
				Title:         "Test PR",
				Author:        "testuser",
				HTMLURL:       "https://github.com/testorg/testrepo/pull/42",
				State:         "newly_published",
				WorkflowState: "",
			},
			expectedAction: "newly published",
			shouldContain:  []string{"Test PR", "testrepo#42", "testuser", "newly published"},
		},
		{
			name: "tests_broken_state",
			pr: PRInfo{
				Owner:         "testorg",
				Repo:          "testrepo",
				Number:        42,
				Title:         "Fix bug",
				Author:        "developer",
				HTMLURL:       "https://github.com/testorg/testrepo/pull/42",
				State:         "tests_broken",
				WorkflowState: "",
			},
			expectedAction: "fix tests",
			shouldContain:  []string{"Fix bug", "testrepo#42", "developer", "fix tests"},
		},
		{
			name: "awaiting_review_state",
			pr: PRInfo{
				Owner:         "testorg",
				Repo:          "testrepo",
				Number:        42,
				Title:         "Add feature",
				Author:        "contributor",
				HTMLURL:       "https://github.com/testorg/testrepo/pull/42",
				State:         "awaiting_review",
				WorkflowState: "",
			},
			expectedAction: "review",
			shouldContain:  []string{"Add feature", "testrepo#42", "contributor", "review"},
		},
		{
			name: "changes_requested_state",
			pr: PRInfo{
				Owner:         "testorg",
				Repo:          "testrepo",
				Number:        42,
				Title:         "Update docs",
				Author:        "writer",
				HTMLURL:       "https://github.com/testorg/testrepo/pull/42",
				State:         "changes_requested",
				WorkflowState: "",
			},
			expectedAction: "address feedback",
			shouldContain:  []string{"Update docs", "testrepo#42", "writer", "address feedback"},
		},
		{
			name: "approved_state",
			pr: PRInfo{
				Owner:         "testorg",
				Repo:          "testrepo",
				Number:        42,
				Title:         "Ready to merge",
				Author:        "maintainer",
				HTMLURL:       "https://github.com/testorg/testrepo/pull/42",
				State:         "approved",
				WorkflowState: "",
			},
			expectedAction: "merge",
			shouldContain:  []string{"Ready to merge", "testrepo#42", "maintainer", "merge"},
		},
		{
			name: "default_unknown_state",
			pr: PRInfo{
				Owner:         "testorg",
				Repo:          "testrepo",
				Number:        42,
				Title:         "Unknown state PR",
				Author:        "someone",
				HTMLURL:       "https://github.com/testorg/testrepo/pull/42",
				State:         "some_unknown_state",
				WorkflowState: "",
			},
			expectedAction: "attention needed",
			shouldContain:  []string{"Unknown state PR", "testrepo#42", "someone", "attention needed"},
		},
		{
			name: "with_workflow_state",
			pr: PRInfo{
				Owner:         "testorg",
				Repo:          "testrepo",
				Number:        42,
				Title:         "Workflow PR",
				Author:        "dev",
				HTMLURL:       "https://github.com/testorg/testrepo/pull/42",
				State:         "awaiting_review",
				WorkflowState: "tests_broken",
				NextAction: map[string]turn.Action{
					"alice": {Kind: turn.ActionFixTests},
				},
			},
			expectedAction: "review",
			shouldContain:  []string{"Workflow PR", "testrepo#42", "dev", "review"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, action := formatDMMessage(tt.pr)

			if action != tt.expectedAction {
				t.Errorf("formatDMMessage() action = %v, want %v", action, tt.expectedAction)
			}

			for _, expected := range tt.shouldContain {
				if !strings.Contains(message, expected) {
					t.Errorf("formatDMMessage() message = %v, should contain %v", message, expected)
				}
			}

			// Verify message format structure
			if !strings.Contains(message, "→") {
				t.Error("formatDMMessage() message should contain '→' separator")
			}
			if !strings.Contains(message, "·") {
				t.Error("formatDMMessage() message should contain '·' separator")
			}
		})
	}
}
