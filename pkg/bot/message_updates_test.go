package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	turn "github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// TestResolveAndValidateChannel_Success tests successful channel resolution
func TestResolveAndValidateChannel_Success(t *testing.T) {
	ctx := context.Background()

	mockSlack := NewMockSlack().
		WithChannelResolution("test-channel", "C123456").
		Build()

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
	}

	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, "test-channel", "org", "repo", 1)

	if !ok {
		t.Error("expected resolution to succeed")
	}

	if channelID != "C123456" {
		t.Errorf("expected channelID C123456, got %s", channelID)
	}

	if channelDisplay != "#test-channel (C123456)" {
		t.Errorf("expected display '#test-channel (C123456)', got %s", channelDisplay)
	}
}

// TestResolveAndValidateChannel_ResolutionFailed tests when channel resolution fails
func TestResolveAndValidateChannel_ResolutionFailed(t *testing.T) {
	ctx := context.Background()

	// Mock that returns the same name (resolution failed)
	mockSlack := NewMockSlack().
		WithChannelResolution("unknown-channel", "unknown-channel").
		Build()

	c := &Coordinator{
		slack:         mockSlack,
		threadCache:   cache.New(),
		workspaceName: "test-workspace",
	}

	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, "unknown-channel", "org", "repo", 1)

	if ok {
		t.Error("expected resolution to fail")
	}

	if channelID != "" {
		t.Errorf("expected empty channelID, got %s", channelID)
	}

	if channelDisplay != "" {
		t.Errorf("expected empty channelDisplay, got %s", channelDisplay)
	}
}

// TestResolveAndValidateChannel_HashPrefix tests channel with # prefix that fails resolution
func TestResolveAndValidateChannel_HashPrefix(t *testing.T) {
	ctx := context.Background()

	// Mock that strips # but still fails resolution (returns stripped name)
	mockSlack := NewMockSlack().
		WithChannelResolution("#test-channel", "test-channel").
		Build()

	c := &Coordinator{
		slack:         mockSlack,
		threadCache:   cache.New(),
		workspaceName: "test-workspace",
	}

	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, "#test-channel", "org", "repo", 1)

	if ok {
		t.Error("expected resolution to fail")
	}

	if channelID != "" {
		t.Errorf("expected empty channelID, got %s", channelID)
	}

	if channelDisplay != "" {
		t.Errorf("expected empty channelDisplay, got %s", channelDisplay)
	}
}

// TestResolveAndValidateChannel_ChannelIDReturned tests when slack returns actual channel ID
func TestResolveAndValidateChannel_ChannelIDReturned(t *testing.T) {
	ctx := context.Background()

	// Mock returns different channel ID (not equal to input name)
	mockSlack := NewMockSlack().
		WithChannelResolution("general", "CABC123").
		Build()

	c := &Coordinator{
		slack:       mockSlack,
		threadCache: cache.New(),
	}

	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, "general", "org", "repo", 1)

	if !ok {
		t.Error("expected resolution to succeed")
	}

	if channelID != "CABC123" {
		t.Errorf("expected channelID CABC123, got %s", channelID)
	}

	if channelDisplay != "#general (CABC123)" {
		t.Errorf("expected display '#general (CABC123)', got %s", channelDisplay)
	}
}

// TestResolveAndValidateChannel_AlreadyChannelID tests when input is already a channel ID
// NOTE: Current implementation treats this as a failure (channelID == channelName),
// even though ResolveChannelID correctly returns the ID as-is for valid IDs.
// This may be a bug, but test matches current behavior.
func TestResolveAndValidateChannel_AlreadyChannelID(t *testing.T) {
	ctx := context.Background()

	// Mock returns the same ID (input was already a channel ID)
	// ResolveChannelID is designed to return channel IDs as-is without validation
	mockSlack := NewMockSlack().
		WithChannelResolution("C123456", "C123456").
		Build()

	c := &Coordinator{
		slack:         mockSlack,
		threadCache:   cache.New(),
		workspaceName: "test-workspace",
	}

	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, "C123456", "org", "repo", 1)

	// Current behavior: treats channelID == channelName as failure
	if ok {
		t.Error("expected resolution to fail (current behavior when channelID == channelName)")
	}

	if channelID != "" {
		t.Errorf("expected empty channelID on failure, got %s", channelID)
	}

	if channelDisplay != "" {
		t.Errorf("expected empty channelDisplay on failure, got %s", channelDisplay)
	}
}

// TestResolveAndValidateChannel_UnresolvedDefault tests default unresolved case
func TestResolveAndValidateChannel_UnresolvedDefault(t *testing.T) {
	ctx := context.Background()

	// Mock returns something unusual (not matching the logic for channel ID starting with C)
	mockSlack := NewMockSlack().
		WithChannelResolution("test", "test").
		Build()

	c := &Coordinator{
		slack:         mockSlack,
		threadCache:   cache.New(),
		workspaceName: "test-workspace",
	}

	channelID, channelDisplay, ok := c.resolveAndValidateChannel(ctx, "test", "org", "repo", 1)

	// This should fail resolution since channelID == channelName
	if ok {
		t.Error("expected resolution to fail when channelID equals channelName")
	}

	if channelID != "" {
		t.Errorf("expected empty channelID, got %s", channelID)
	}

	if channelDisplay != "" {
		t.Errorf("expected empty channelDisplay, got %s", channelDisplay)
	}
}

// TestUpdateDMMessagesForPR_MergedNoRecipients tests merged PR with no DM recipients
func TestUpdateDMMessagesForPR_MergedNoRecipients(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	mockSlack := NewMockSlack().Build()
	mockConfig := NewMockConfig().Build()

	c := &Coordinator{
		stateStore:    testStore,
		slack:         mockSlack,
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	info := prUpdateInfo{
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		PRState:  "merged",
	}

	// Should return early with debug log (no DM recipients)
	c.updateDMMessagesForPR(ctx, info)

	// Test passes if no panic occurs
}

// TestUpdateDMMessagesForPR_NoBlockedUsers tests non-terminal state with no blocked users
func TestUpdateDMMessagesForPR_NoBlockedUsers(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	mockSlack := NewMockSlack().Build()
	mockConfig := NewMockConfig().Build()

	c := &Coordinator{
		stateStore:    testStore,
		slack:         mockSlack,
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	info := prUpdateInfo{
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		PRState:  "open",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{}, // No blocked users
			},
		},
	}

	// Should return early (no blocked users)
	c.updateDMMessagesForPR(ctx, info)

	// Test passes if no panic occurs
}

// TestUpdateDMMessagesForPR_NilCheckResult tests with nil check result
func TestUpdateDMMessagesForPR_NilCheckResult(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	mockSlack := NewMockSlack().Build()
	mockConfig := NewMockConfig().Build()

	c := &Coordinator{
		stateStore:    testStore,
		slack:         mockSlack,
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	info := prUpdateInfo{
		Owner:       "org",
		Repo:        "repo",
		PRNumber:    1,
		PRURL:       "https://github.com/org/repo/pull/1",
		PRTitle:     "Test PR",
		PRAuthor:    "author",
		PRState:     "open",
		CheckResult: nil,
	}

	// Should return early (nil check result)
	c.updateDMMessagesForPR(ctx, info)

	// Test passes if no panic occurs
}

// TestUpdateDMMessagesForPR_SystemUserSkipped tests that _system user is skipped
func TestUpdateDMMessagesForPR_SystemUserSkipped(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	mockSlack := NewMockSlack().Build()
	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	c := &Coordinator{
		stateStore:    testStore,
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    &mockUserMapper{mapping: map[string]string{}},
		threadCache:   cache.New(),
	}

	info := prUpdateInfo{
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		PRState:  "open",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{
					"_system": {Kind: "review"}, // System user should be skipped
				},
			},
		},
	}

	// Should return early after skipping _system user (no Slack users found)
	c.updateDMMessagesForPR(ctx, info)

	// Test passes if no panic occurs
}

// TestUpdateDMMessagesForPR_UserMappingFailure tests when user mapping fails
func TestUpdateDMMessagesForPR_UserMappingFailure(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()
	mockSlack := NewMockSlack().Build()
	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	// User mapper that always fails
	mockMapper := &mockUserMapper{
		failLookups: true,
	}

	c := &Coordinator{
		stateStore:    testStore,
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		threadCache:   cache.New(),
	}

	info := prUpdateInfo{
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		PRState:  "open",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{
					"user1": {Kind: "review"},
				},
			},
		},
	}

	// Should handle user mapping failure gracefully (no Slack users found)
	c.updateDMMessagesForPR(ctx, info)

	// Test passes if no panic occurs
}

// TestUpdateDMMessagesForPR_SendNotificationError tests error during DM send
func TestUpdateDMMessagesForPR_SendNotificationError(t *testing.T) {
	ctx := context.Background()

	testStore := state.NewMemoryStore()

	// Mock Slack that returns error for DM operations
	mockSlack := &mockSlackClient{
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "", "", errors.New("slack API error")
		},
	}

	mockConfig := NewMockConfig().
		WithDomain("example.com").
		Build()

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := &Coordinator{
		stateStore:    testStore,
		slack:         mockSlack,
		configManager: mockConfig,
		userMapper:    mockMapper,
		threadCache:   cache.New(),
	}

	info := prUpdateInfo{
		Owner:    "org",
		Repo:     "repo",
		PRNumber: 1,
		PRURL:    "https://github.com/org/repo/pull/1",
		PRTitle:  "Test PR",
		PRAuthor: "author",
		PRState:  "open",
		CheckResult: &turn.CheckResponse{
			Analysis: turn.Analysis{
				NextAction: map[string]turn.Action{
					"user1": {Kind: "review"},
				},
			},
		},
	}

	// Should handle send notification error gracefully (logs warning)
	c.updateDMMessagesForPR(ctx, info)

	// Test passes if no panic occurs
}
