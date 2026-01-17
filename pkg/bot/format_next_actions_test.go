package bot

import (
	"context"
	"strings"
	"testing"

	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestFormatNextActions_SystemUserOnly tests when only _system user has action
func TestFormatNextActions_SystemUserOnly(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{},
	}

	c := &Coordinator{
		userMapper: mockMapper,
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"_system": {Kind: "merge"},
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "org", "example.com")

	// Should show action without users
	if result != "merge" {
		t.Errorf("expected 'merge', got %s", result)
	}
}

// TestFormatNextActions_SingleUser tests single user action
func TestFormatNextActions_SingleUser(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			if githubUser == "user1" {
				return "U123", nil
			}
			return "", nil
		},
	}

	c := &Coordinator{
		userMapper: mockMapper,
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "org", "example.com")

	// Should format as "review: <@U123>"
	if !strings.Contains(result, "review:") {
		t.Errorf("expected 'review:' in result, got %s", result)
	}
}

// TestFormatNextActions_MultipleUsersOneAction tests multiple users with same action
func TestFormatNextActions_MultipleUsersOneAction(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
			"user2": "U456",
		},
	}

	c := &Coordinator{
		userMapper: mockMapper,
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
				"user2": {Kind: "review"},
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "org", "example.com")

	// Should format as "review: <@U123>, <@U456>" (or vice versa, order not guaranteed)
	if !strings.Contains(result, "review:") {
		t.Errorf("expected 'review:' in result, got %s", result)
	}
	if !strings.Contains(result, "U123") {
		t.Errorf("expected U123 in result, got %s", result)
	}
	if !strings.Contains(result, "U456") {
		t.Errorf("expected U456 in result, got %s", result)
	}
}

// TestFormatNextActions_MultipleActions tests multiple different actions
func TestFormatNextActions_MultipleActions(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
			"user2": "U456",
		},
	}

	c := &Coordinator{
		userMapper: mockMapper,
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
				"user2": {Kind: "fix_tests"},
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "org", "example.com")

	// Should format with semicolons separating different actions
	if !strings.Contains(result, ";") {
		t.Errorf("expected ';' separator for multiple actions, got %s", result)
	}

	// Should contain both actions (snake_case converted to space-separated)
	if !strings.Contains(result, "review") {
		t.Errorf("expected 'review' in result, got %s", result)
	}
	if !strings.Contains(result, "fix tests") {
		t.Errorf("expected 'fix tests' in result, got %s", result)
	}
}

// TestFormatNextActions_MixedSystemAndRealUsers tests system + real users
func TestFormatNextActions_MixedSystemAndRealUsers(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := &Coordinator{
		userMapper: mockMapper,
	}

	checkResult := &turn.CheckResponse{
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1":   {Kind: "review"},
				"_system": {Kind: "review"}, // System user should be filtered out
			},
		},
	}

	result := c.formatNextActions(ctx, checkResult, "org", "example.com")

	// Should show real user's action
	if !strings.Contains(result, "review:") {
		t.Errorf("expected 'review:' in result, got %s", result)
	}
	if !strings.Contains(result, "U123") {
		t.Errorf("expected U123 in result, got %s", result)
	}
}
