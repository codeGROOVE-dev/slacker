package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	ghmailto "github.com/codeGROOVE-dev/gh-mailto/pkg/gh-mailto"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/slacktest"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	slackapi "github.com/slack-go/slack"
)

// TestUserMappingIntegration tests the complete flow of mapping GitHub users to Slack users.
func TestUserMappingIntegration(t *testing.T) {
	// Setup mock Slack server
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	// Add test users
	mockSlack.AddUser("alice@example.com", "U001", "alice")
	ctx := context.Background()

	mockSlack.AddUser("bob@example.com", "U002", "bob")

	// Create Slack client pointing to mock server
	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))

	// Create mock GitHub email lookup
	mockGitHub := &mockGitHubLookup{
		emails: map[string][]string{
			"alice":   {"alice@example.com"},
			"bob":     {"bob@example.com", "bob@personal.com"},
			"charlie": {"charlie@other.com"}, // Won't be found in Slack
		},
	}

	// Create user mapper
	userMapper := usermapping.NewForTesting(slackClient, mockGitHub)

	tests := []struct {
		name           string
		githubUsername string
		wantSlackID    string
		wantErr        bool
	}{
		{
			name:           "successful mapping with single email",
			githubUsername: "alice",
			wantSlackID:    "U001",
			wantErr:        false,
		},
		{
			name:           "successful mapping with multiple emails",
			githubUsername: "bob",
			wantSlackID:    "U002",
			wantErr:        false,
		},
		{
			name:           "no mapping - email not in Slack",
			githubUsername: "charlie",
			wantSlackID:    "",
			wantErr:        false, // Not an error, just no mapping
		},
		{
			name:           "no mapping - no GitHub emails found",
			githubUsername: "unknown",
			wantSlackID:    "",
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slackID, err := userMapper.SlackHandle(ctx, tt.githubUsername, "test-org", "example.com")

			if (err != nil) != tt.wantErr {
				t.Errorf("SlackHandle() error = %v, wantErr %v", err, tt.wantErr)
			}

			if slackID != tt.wantSlackID {
				t.Errorf("SlackHandle() = %v, want %v", slackID, tt.wantSlackID)
			}
		})
	}

	// Verify email lookups were performed
	emailLookups := mockSlack.EmailLookups()
	if len(emailLookups) < 3 {
		t.Errorf("Expected at least 3 email lookups, got %d", len(emailLookups))
	}
}

// TestTurnclientEmojiIntegration tests the complete emoji determination pipeline.
func TestTurnclientEmojiIntegration(t *testing.T) {
	tests := []struct {
		name           string
		workflowState  string
		nextActions    map[string]turn.Action
		expectedEmoji  string
		expectedAction string
		description    string
	}{
		{
			name:          "newly published takes precedence",
			workflowState: string(turn.StateNewlyPublished),
			nextActions: map[string]turn.Action{
				"author": {Kind: turn.ActionFixTests},
			},
			expectedEmoji:  ":new:",
			expectedAction: "fix_tests",
			description:    "newly_published state should always show :new: even with other actions",
		},
		{
			name:          "draft needs publishing",
			workflowState: "awaiting_action",
			nextActions: map[string]turn.Action{
				"author": {Kind: turn.ActionPublishDraft},
			},
			expectedEmoji:  ":construction:",
			expectedAction: "publish_draft",
			description:    "unpublished draft should show construction emoji",
		},
		{
			name:          "tests broken has highest action priority",
			workflowState: "blocked",
			nextActions: map[string]turn.Action{
				"author":   {Kind: turn.ActionFixTests},
				"reviewer": {Kind: turn.ActionReview},
			},
			expectedEmoji:  ":cockroach:",
			expectedAction: "fix_tests",
			description:    "fix_tests should take priority over review",
		},
		{
			name:          "awaiting review",
			workflowState: "awaiting_review",
			nextActions: map[string]turn.Action{
				"reviewer": {Kind: turn.ActionReview},
			},
			expectedEmoji:  ":hourglass:",
			expectedAction: "review",
			description:    "review action should show hourglass",
		},
		{
			name:          "ready to merge",
			workflowState: "approved",
			nextActions: map[string]turn.Action{
				"author": {Kind: turn.ActionMerge},
			},
			expectedEmoji:  ":rocket:",
			expectedAction: "merge",
			description:    "merge action should show rocket",
		},
		{
			name:           "merged with no actions shows fallback",
			workflowState:  "merged",
			nextActions:    map[string]turn.Action{},
			expectedEmoji:  ":postal_horn:",
			expectedAction: "",
			description:    "This is a known bug - merged state with no actions falls back to postal_horn",
		},
		{
			name:          "multiple reviewers prioritized correctly",
			workflowState: "awaiting_review",
			nextActions: map[string]turn.Action{
				"alice": {Kind: turn.ActionReview},
				"bob":   {Kind: turn.ActionApprove},
				"carol": {Kind: turn.ActionReview},
			},
			expectedEmoji:  ":hourglass:",
			expectedAction: "review",
			description:    "review has lower priority number (6) than approve (7)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test emoji determination
			emoji := notify.PrefixForAnalysis(tt.workflowState, tt.nextActions)
			if emoji != tt.expectedEmoji {
				t.Errorf("%s: PrefixForAnalysis() = %q, want %q",
					tt.description, emoji, tt.expectedEmoji)
			}

			// Test primary action extraction
			primaryAction := notify.PrimaryAction(tt.nextActions)
			if primaryAction != tt.expectedAction {
				t.Errorf("%s: PrimaryAction() = %q, want %q",
					tt.description, primaryAction, tt.expectedAction)
			}

			// Verify consistency: if there's a primary action, its emoji should match
			// (except for newly_published and merged edge cases)
			if tt.workflowState != string(turn.StateNewlyPublished) && tt.workflowState != "merged" && tt.expectedAction != "" {
				actionEmoji := notify.PrefixForAction(tt.expectedAction)
				if emoji != actionEmoji {
					t.Errorf("%s: emoji mismatch - PrefixForAnalysis=%q but PrefixForAction(%q)=%q",
						tt.description, emoji, tt.expectedAction, actionEmoji)
				}
			}
		})
	}
}

// TestDMDelayLogicIntegration tests the DM delay decision logic.
func TestDMDelayLogicIntegration(t *testing.T) {
	ctx := context.Background()

	// Setup mock Slack server
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddUser("alice@example.com", "U001", "alice")
	mockSlack.AddChannel("C123", "dev", true)
	mockSlack.AddChannelMember("C123", "U001") // alice is in channel
	mockSlack.AddChannel("C456", "other", true)
	// alice is NOT in C456

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))

	// Create notification manager
	slackManager := slack.NewManager("")
	slackManager.RegisterWorkspace(ctx, "test-workspace", slackClient)

	configMgr := &mockConfigManager{
		dmDelay: 65, // 65 minute delay
	}

	// Create in-memory store for pending DMs
	store := state.NewMemoryStore()

	notifier := notify.New(notify.WrapSlackManager(slackManager), configMgr, store)

	prInfo := notify.PRInfo{
		Owner:         "test",
		Repo:          "repo",
		Number:        1,
		Title:         "Test PR",
		Author:        "author",
		State:         "awaiting_review",
		HTMLURL:       "https://github.com/test/repo/pull/1",
		WorkflowState: "awaiting_review",
		NextAction: map[string]turn.Action{
			"alice": {Kind: turn.ActionReview},
		},
	}

	tests := []struct {
		name        string
		setupFunc   func()
		channelID   string
		expectDM    bool
		description string
	}{
		{
			name: "user in channel - delay should apply",
			setupFunc: func() {
				notifier.Tracker.UpdateUserPRChannelTag("test-workspace", "U001", "C123", "test", "repo", 1)
			},
			channelID:   "C123",
			expectDM:    false,
			description: "User is in channel where tagged - DM should be delayed",
		},
		{
			name: "user not in channel - immediate DM",
			setupFunc: func() {
				notifier.Tracker.UpdateUserPRChannelTag("test-workspace", "U001", "C456", "test", "repo", 1)
			},
			channelID:   "C456",
			expectDM:    true,
			description: "User is NOT in channel where tagged - DM should be immediate",
		},
		{
			name: "no channel tag - immediate DM",
			setupFunc: func() {
				// Don't record any channel tag
			},
			channelID:   "",
			expectDM:    true,
			description: "No channel tag recorded - DM should be immediate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset state
			mockSlack.Reset()

			// Create fresh tracker with initialized maps
			notifier.Tracker = &notify.NotificationTracker{}
			// Initialize the tracker by creating a new notifier
			store = state.NewMemoryStore() // Reset store as well
			notifier = notify.New(notify.WrapSlackManager(slackManager), configMgr, store)

			// Setup test scenario
			if tt.setupFunc != nil {
				tt.setupFunc()
			}

			// Execute
			err := notifier.NotifyUser(ctx, "test-workspace", "U001", tt.channelID, "dev", prInfo)
			if err != nil {
				t.Errorf("NotifyUser() error = %v", err)
			}

			// Give async operations time to complete
			time.Sleep(50 * time.Millisecond)

			// Verify
			postedMessages := mockSlack.PostedMessages()
			dmCount := 0
			for _, msg := range postedMessages {
				if strings.HasPrefix(msg.Channel, "D") {
					dmCount++
				}
			}

			if tt.expectDM && dmCount == 0 {
				t.Errorf("%s: expected DM to be sent, but none were sent", tt.description)
			}

			if !tt.expectDM && dmCount > 0 {
				t.Errorf("%s: expected no DM (delay should apply), but %d DMs were sent", tt.description, dmCount)
			}
		})
	}
}

// Mock implementations for testing

type mockGitHubLookup struct {
	emails map[string][]string
}

func (m *mockGitHubLookup) Lookup(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
	emails, exists := m.emails[username]
	if !exists {
		return &ghmailto.Result{
			Username:  username,
			Addresses: []ghmailto.Address{},
		}, nil
	}

	addresses := make([]ghmailto.Address, len(emails))
	for i, email := range emails {
		addresses[i] = ghmailto.Address{
			Email:   email,
			Methods: []string{"Test Mock"},
		}
	}

	return &ghmailto.Result{
		Username:  username,
		Addresses: addresses,
	}, nil
}

func (m *mockGitHubLookup) Guess(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error) {
	return &ghmailto.GuessResult{
		Username: username,
		Guesses:  []ghmailto.Address{},
	}, nil
}

type mockConfigManager struct {
	dmDelay      int
	channelsFunc func(org, repo string) []string
	workspace    string
	domain       string
	configData   map[string]interface{}
	loadErr      error
}

func (m *mockConfigManager) DailyRemindersEnabled(org string) bool {
	return false
}

func (m *mockConfigManager) ReminderDMDelay(org, channel string) int {
	return m.dmDelay
}

func (m *mockConfigManager) ChannelsForRepo(org, repo string) []string {
	if m.channelsFunc != nil {
		return m.channelsFunc(org, repo)
	}
	return []string{}
}

func (m *mockConfigManager) LoadConfig(ctx context.Context, org string) error {
	if m.loadErr != nil {
		return m.loadErr
	}
	return nil // Always succeed
}

func (m *mockConfigManager) WorkspaceName(org string) string {
	if m.workspace != "" {
		return m.workspace
	}
	return "test-workspace.slack.com"
}

func (m *mockConfigManager) Domain(org string) string {
	if m.domain != "" {
		return m.domain
	}
	return "test.com"
}

func (m *mockConfigManager) Config(org string) (*config.RepoConfig, bool) {
	if m.configData != nil {
		if cfg, ok := m.configData[org]; ok {
			if repoCfg, ok := cfg.(*config.RepoConfig); ok {
				return repoCfg, true
			}
		}
	}
	return nil, false
}

func (m *mockConfigManager) ReloadConfig(ctx context.Context, org string) error {
	return m.LoadConfig(ctx, org)
}

func (m *mockConfigManager) SetGitHubClient(org string, client any) {
	// No-op for mock
}

func (m *mockConfigManager) SetWorkspaceName(workspaceName string) {
	m.workspace = workspaceName
}
