package bot

import (
	"testing"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestExtractBlockedUsersFromTurnclient verifies that _system users are filtered out.
func TestExtractBlockedUsersFromTurnclient(t *testing.T) {
	tests := []struct {
		name     string
		input    *turn.CheckResponse
		expected []string
	}{
		{
			name: "filters out _system user",
			input: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{
						"_system": {Kind: "review"},
						"alice":   {Kind: "review"},
						"bob":     {Kind: "approve"},
					},
				},
			},
			expected: []string{"alice", "bob"},
		},
		{
			name: "handles only _system user",
			input: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{
						"_system": {Kind: "review"},
					},
				},
			},
			expected: []string{},
		},
		{
			name: "handles no _system user",
			input: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{
						"alice": {Kind: "review"},
						"bob":   {Kind: "approve"},
					},
				},
			},
			expected: []string{"alice", "bob"},
		},
		{
			name: "handles empty next action",
			input: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{},
				},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Coordinator{}
			result := c.extractBlockedUsersFromTurnclient(tt.input)

			// Check length
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d blocked users, got %d", len(tt.expected), len(result))
			}

			// Convert to map for order-independent comparison
			resultMap := make(map[string]bool)
			for _, user := range result {
				resultMap[user] = true
			}

			// Verify all expected users are present
			for _, expectedUser := range tt.expected {
				if !resultMap[expectedUser] {
					t.Errorf("expected user %q not found in result", expectedUser)
				}
			}

			// Verify no _system user in result
			for _, user := range result {
				if user == "_system" {
					t.Errorf("_system user should be filtered out but was found in result")
				}
			}
		})
	}
}
