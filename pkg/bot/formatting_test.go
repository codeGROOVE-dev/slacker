package bot

import (
	"context"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestFormatNextActions_NilCheckResult(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		userMapper:    usermapping.New(nil, ""),
		configManager: config.New(),
	}

	result := c.formatNextActions(ctx, nil, "testorg", "test.com")
	if result != "" {
		t.Errorf("expected empty string for nil checkResult, got: %s", result)
	}
}

func TestFormatNextActions_EmptyNextAction(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		userMapper:    usermapping.New(nil, ""),
		configManager: config.New(),
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "testorg", "test.com")
	if result != "" {
		t.Errorf("expected empty string for empty NextAction, got: %s", result)
	}
}

func TestFormatNextActions_SystemUser(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		userMapper:    usermapping.New(nil, ""),
		configManager: config.New(),
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"_system": {Kind: "fix"},
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "testorg", "test.com")
	// When only _system, should just show the action name
	if result != "fix" {
		t.Errorf("expected 'fix', got: %s", result)
	}
}

func TestFormatNextActions_SnakeCaseConversion(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		userMapper:    usermapping.New(nil, ""),
		configManager: config.New(),
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"_system": {Kind: "address_review_comments"},
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "testorg", "test.com")
	// Snake_case should be converted to spaces
	if result != "address review comments" {
		t.Errorf("expected 'address review comments', got: %s", result)
	}
}

func TestFormatNextActions_MultipleSystemActions(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		userMapper:    usermapping.New(nil, ""),
		configManager: config.New(),
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"_system": {Kind: "fix"},
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "testorg", "test.com")
	// When only _system user, should show just the action
	if result != "fix" {
		t.Errorf("expected 'fix', got: %s", result)
	}
}
