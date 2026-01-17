package bot

import (
	"context"
	"testing"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestTrackUserTagsForDMDelay_NoBlockedUsers tests early return when no users are blocked
func TestTrackUserTagsForDMDelay_NoBlockedUsers(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		configManager: NewMockConfig().Build(),
	}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{}, // No blocked users
		},
	}

	tags := c.trackUserTagsForDMDelay(ctx, "W123", "C123", "#test-channel", "org", "repo", 1, checkResult)

	if len(tags) != 0 {
		t.Errorf("expected empty tags for no blocked users, got %d tags", len(tags))
	}
}

// TestTrackUserTagsForDMDelay_UserMappingFailure tests when user mapping fails
func TestTrackUserTagsForDMDelay_UserMappingFailure(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		failLookups: true, // All lookups fail
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	c := &Coordinator{
		userMapper:    mockMapper,
		configManager: mockConfig,
	}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
			},
		},
	}

	tags := c.trackUserTagsForDMDelay(ctx, "W123", "C123", "#test-channel", "org", "repo", 1, checkResult)

	// Should return empty map when user mapping fails
	if len(tags) != 0 {
		t.Errorf("expected empty tags when user mapping fails, got %d tags", len(tags))
	}
}

// TestTrackUserTagsForDMDelay_EmptySlackUserID tests when mapping returns empty slack ID
func TestTrackUserTagsForDMDelay_EmptySlackUserID(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "", // Empty Slack ID
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	c := &Coordinator{
		userMapper:    mockMapper,
		configManager: mockConfig,
	}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
			},
		},
	}

	tags := c.trackUserTagsForDMDelay(ctx, "W123", "C123", "#test-channel", "org", "repo", 1, checkResult)

	// Should skip users with empty Slack IDs
	if len(tags) != 0 {
		t.Errorf("expected empty tags for empty slack user ID, got %d tags", len(tags))
	}
}

// TestTrackUserTagsForDMDelay_Success tests successful user tagging
func TestTrackUserTagsForDMDelay_Success(t *testing.T) {
	ctx := context.Background()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
			"user2": "U456",
		},
	}

	mockSlack := &mockSlackClient{
		isUserInChannelFunc: func(ctx context.Context, channelID, userID string) bool {
			// user1 is in channel, user2 is not
			return userID == "U123"
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	c := &Coordinator{
		userMapper:    mockMapper,
		slack:         mockSlack,
		configManager: mockConfig,
		notifier:      nil, // Testing without notifier for simplicity
	}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{State: "open"},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				"user1": {Kind: "review"},
				"user2": {Kind: "approve"},
			},
		},
	}

	tags := c.trackUserTagsForDMDelay(ctx, "W123", "C123", "#test-channel", "org", "repo", 1, checkResult)

	if len(tags) != 2 {
		t.Fatalf("expected 2 tagged users, got %d", len(tags))
	}

	// Check user1
	if tag, exists := tags["U123"]; !exists {
		t.Error("expected U123 in tagged users")
	} else {
		if tag.UserID != "U123" {
			t.Errorf("expected UserID U123, got %s", tag.UserID)
		}
		if !tag.IsInAnyChannel {
			t.Error("expected U123 to be in channel")
		}
	}

	// Check user2
	if tag, exists := tags["U456"]; !exists {
		t.Error("expected U456 in tagged users")
	} else {
		if tag.UserID != "U456" {
			t.Errorf("expected UserID U456, got %s", tag.UserID)
		}
		if tag.IsInAnyChannel {
			t.Error("expected U456 to NOT be in channel")
		}
	}
}

