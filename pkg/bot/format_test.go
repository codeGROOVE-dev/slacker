package bot

import (
	"context"
	"testing"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestFormatNextActionsEarlyReturn tests the formatNextActions utility function early return cases.
func TestFormatNextActionsEarlyReturn(t *testing.T) {
	ctx := context.Background()
	// Create a coordinator - userMapper will be nil but that's OK for these early return tests
	c := &Coordinator{}

	tests := []struct {
		name     string
		check    *turn.CheckResponse
		owner    string
		domain   string
		expected string
	}{
		{
			name:     "nil check response",
			check:    nil,
			owner:    "org",
			domain:   "example.com",
			expected: "",
		},
		{
			name: "empty next actions",
			check: &turn.CheckResponse{
				Analysis: turn.Analysis{
					NextAction: map[string]turn.Action{},
				},
			},
			owner:    "org",
			domain:   "example.com",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.formatNextActions(ctx, tt.check, tt.owner, tt.domain)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
