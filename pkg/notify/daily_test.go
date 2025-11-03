package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/home"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

func TestFormatDigestMessage(t *testing.T) {
	tests := []struct {
		name     string
		time     time.Time
		incoming []home.PR
		outgoing []home.PR
		expected string
	}{
		{
			name: "incoming PRs only at 8:30am",
			time: time.Date(2025, 10, 22, 8, 30, 0, 0, time.UTC),
			incoming: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/slacker/pull/123",
					Title:      "Add daily digest feature",
					Author:     "otheruser",
					ActionKind: "review",
				},
			},
			outgoing: nil,
			expected: `☀️ *Good morning!*

*To Review:*
:hourglass: <https://github.com/codeGROOVE-dev/slacker/pull/123|Add daily digest feature> · review

_Your daily digest from Ready to Review_`,
		},
		{
			name:     "outgoing PRs only at 8:15am",
			time:     time.Date(2025, 10, 22, 8, 15, 0, 0, time.UTC),
			incoming: nil,
			outgoing: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/slacker/pull/124",
					Title:      "Fix authentication bug",
					Author:     "testuser",
					ActionKind: "address-feedback",
				},
			},
			expected: `🌻 *Hello sunshine!*

*Your PRs:*
:hourglass: <https://github.com/codeGROOVE-dev/slacker/pull/124|Fix authentication bug> · address-feedback

_Your daily digest from Ready to Review_`,
		},
		{
			name: "both incoming and outgoing at 8:45am",
			time: time.Date(2025, 10, 22, 8, 45, 0, 0, time.UTC),
			incoming: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/goose/pull/456",
					Title:      "Implement new API endpoint",
					Author:     "colleague",
					ActionKind: "review",
				},
				{
					URL:        "https://github.com/codeGROOVE-dev/goose/pull/457",
					Title:      "Refactor database layer",
					Author:     "teammate",
					ActionKind: "approve",
				},
			},
			outgoing: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/goose/pull/458",
					Title:      "Update documentation",
					Author:     "testuser",
					ActionKind: "merge",
				},
			},
			expected: `🌻 *Hello sunshine!*

*To Review:*
:hourglass: <https://github.com/codeGROOVE-dev/goose/pull/456|Implement new API endpoint> · review
:hourglass: <https://github.com/codeGROOVE-dev/goose/pull/457|Refactor database layer> · approve

*Your PRs:*
:hourglass: <https://github.com/codeGROOVE-dev/goose/pull/458|Update documentation> · merge

_Your daily digest from Ready to Review_`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler := &DailyDigestScheduler{}
			got := scheduler.formatDigestMessageAt(tt.incoming, tt.outgoing, tt.time)

			if got != tt.expected {
				t.Errorf("formatDigestMessageAt() mismatch\nGot:\n%s\n\nExpected:\n%s", got, tt.expected)
			}
		})
	}
}

func TestDigestMessageVariety(t *testing.T) {
	// Test that different times produce different greetings
	scheduler := &DailyDigestScheduler{}

	incoming := []home.PR{
		{
			URL:        "https://github.com/codeGROOVE-dev/slacker/pull/1",
			Title:      "Test PR",
			Author:     "other",
			ActionKind: "review",
		},
	}

	// Collect messages at different times
	messages := make(map[string]bool)
	for hour := 8; hour < 9; hour++ {
		for minute := 0; minute < 60; minute += 15 {
			testTime := time.Date(2025, 10, 22, hour, minute, 0, 0, time.UTC)
			msg := scheduler.formatDigestMessageAt(incoming, nil, testTime)
			messages[msg] = true
		}
	}

	// Should have at least 2 different message variations in the 8-9am window
	if len(messages) < 2 {
		t.Errorf("Expected message variety, but got only %d unique messages in 1-hour window", len(messages))
	}

	t.Logf("Generated %d unique message variations across the 8-9am window", len(messages))
}

// TestDailyDigestExample shows what an actual daily digest looks like with both sections.
func TestDailyDigestExample(t *testing.T) {
	scheduler := &DailyDigestScheduler{}

	// Example: User has 2 incoming PRs to review and 1 outgoing PR needing attention at 8:30am
	exampleTime := time.Date(2025, 10, 22, 8, 30, 0, 0, time.UTC)

	exampleIncoming := []home.PR{
		{
			URL:        "https://github.com/codeGROOVE-dev/goose/pull/127",
			Title:      "Add support for custom prompts",
			Author:     "colleague",
			ActionKind: "review",
		},
		{
			URL:        "https://github.com/codeGROOVE-dev/sprinkler/pull/15",
			Title:      "Implement WebSocket reconnection logic",
			Author:     "teammate",
			ActionKind: "approve",
		},
	}

	exampleOutgoing := []home.PR{
		{
			URL:        "https://github.com/codeGROOVE-dev/slacker/pull/48",
			Title:      "Update DM messages when PR is merged",
			Author:     "testuser",
			ActionKind: "address-feedback",
		},
	}

	message := scheduler.formatDigestMessageAt(exampleIncoming, exampleOutgoing, exampleTime)

	// Log the example for documentation purposes
	t.Logf("Example daily digest DM:\n\n%s\n", message)

	// Verify it has the expected structure
	if message == "" {
		t.Error("Message should not be empty")
	}

	// Should contain both section headers
	if !strings.Contains(message, "*To Review:*") {
		t.Error("Message should contain 'To Review:' header")
	}
	if !strings.Contains(message, "*Your PRs:*") {
		t.Error("Message should contain 'Your PRs:' header")
	}

	// Should contain all PR URLs
	allPRs := make([]home.PR, 0, len(exampleIncoming)+len(exampleOutgoing))
	allPRs = append(allPRs, exampleIncoming...)
	allPRs = append(allPRs, exampleOutgoing...)
	for _, pr := range allPRs {
		if !strings.Contains(message, pr.URL) {
			t.Errorf("Message should contain PR URL: %s", pr.URL)
		}
	}

	// Should contain footer
	if !strings.Contains(message, "Your daily digest from Ready to Review") {
		t.Error("Message should contain footer")
	}
}

// TestEnrichPR verifies PR enrichment with turnclient data.
func TestEnrichPR(t *testing.T) {
	scheduler := &DailyDigestScheduler{}

	tests := []struct {
		name       string
		pr         home.PR
		action     turn.Action
		wantFields map[string]interface{}
	}{
		{
			name: "review action",
			pr: home.PR{
				Number:     123,
				Title:      "Update README",
				Author:     "alice",
				Repository: "org/repo",
				URL:        "https://github.com/org/repo/pull/123",
			},
			action: turn.Action{
				Kind:   "review",
				Reason: "PR needs review",
			},
			wantFields: map[string]interface{}{
				"ActionKind":   "review",
				"ActionReason": "PR needs review",
				"NeedsReview":  true,
				"IsBlocked":    true,
			},
		},
		{
			name: "approve action",
			pr: home.PR{
				Number: 456,
				Title:  "Add feature",
			},
			action: turn.Action{
				Kind:   "approve",
				Reason: "LGTM but needs approval",
			},
			wantFields: map[string]interface{}{
				"ActionKind":   "approve",
				"ActionReason": "LGTM but needs approval",
				"NeedsReview":  true,
				"IsBlocked":    true,
			},
		},
		{
			name: "address_feedback action",
			pr: home.PR{
				Number: 789,
				Title:  "Fix bug",
			},
			action: turn.Action{
				Kind:   "address_feedback",
				Reason: "Comments need resolution",
			},
			wantFields: map[string]interface{}{
				"ActionKind":   "address_feedback",
				"ActionReason": "Comments need resolution",
				"NeedsReview":  false, // Not a review action
				"IsBlocked":    true,
			},
		},
		{
			name: "merge action",
			pr: home.PR{
				Number: 999,
				Title:  "Ready to merge",
			},
			action: turn.Action{
				Kind:   "merge",
				Reason: "All checks passed",
			},
			wantFields: map[string]interface{}{
				"ActionKind":   "merge",
				"ActionReason": "All checks passed",
				"NeedsReview":  false,
				"IsBlocked":    true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal CheckResponse (only Action is used by enrichPR)
			checkResult := &turn.CheckResponse{
				PullRequest: prx.PullRequest{},
				Analysis:    turn.Analysis{},
			}

			enriched := scheduler.enrichPR(tt.pr, checkResult, "testuser", tt.action)

			// Verify all expected fields
			//nolint:errcheck // Type assertion in test is safe
			if enriched.ActionKind != tt.wantFields["ActionKind"].(string) {
				t.Errorf("ActionKind = %q, want %q", enriched.ActionKind, tt.wantFields["ActionKind"])
			}

			//nolint:errcheck // Type assertion in test is safe
			if enriched.ActionReason != tt.wantFields["ActionReason"].(string) {
				t.Errorf("ActionReason = %q, want %q", enriched.ActionReason, tt.wantFields["ActionReason"])
			}

			//nolint:errcheck // Type assertion in test is safe
			if enriched.NeedsReview != tt.wantFields["NeedsReview"].(bool) {
				t.Errorf("NeedsReview = %v, want %v", enriched.NeedsReview, tt.wantFields["NeedsReview"])
			}

			//nolint:errcheck // Type assertion in test is safe
			if enriched.IsBlocked != tt.wantFields["IsBlocked"].(bool) {
				t.Errorf("IsBlocked = %v, want %v", enriched.IsBlocked, tt.wantFields["IsBlocked"])
			}

			// Verify original fields are preserved
			if enriched.Number != tt.pr.Number {
				t.Errorf("Number = %d, want %d", enriched.Number, tt.pr.Number)
			}
			if enriched.Title != tt.pr.Title {
				t.Errorf("Title = %q, want %q", enriched.Title, tt.pr.Title)
			}
		})
	}
}

// TestEnrichPR_PreservesOriginalFields verifies that enrichment doesn't lose PR data.
func TestEnrichPR_PreservesOriginalFields(t *testing.T) {
	scheduler := &DailyDigestScheduler{}

	originalPR := home.PR{
		Number:     123,
		Title:      "Test PR",
		Author:     "alice",
		Repository: "org/repo",
		URL:        "https://github.com/org/repo/pull/123",
		UpdatedAt:  time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	action := turn.Action{
		Kind:   "review",
		Reason: "Needs review",
	}

	checkResult := &turn.CheckResponse{
		PullRequest: prx.PullRequest{},
		Analysis:    turn.Analysis{},
	}

	enriched := scheduler.enrichPR(originalPR, checkResult, "reviewer", action)

	// Verify all original fields are preserved
	if enriched.Number != originalPR.Number {
		t.Errorf("Number changed: %d -> %d", originalPR.Number, enriched.Number)
	}
	if enriched.Title != originalPR.Title {
		t.Errorf("Title changed: %q -> %q", originalPR.Title, enriched.Title)
	}
	if enriched.Author != originalPR.Author {
		t.Errorf("Author changed: %q -> %q", originalPR.Author, enriched.Author)
	}
	if enriched.Repository != originalPR.Repository {
		t.Errorf("Repository changed: %q -> %q", originalPR.Repository, enriched.Repository)
	}
	if enriched.URL != originalPR.URL {
		t.Errorf("URL changed: %q -> %q", originalPR.URL, enriched.URL)
	}
	if !enriched.UpdatedAt.Equal(originalPR.UpdatedAt) {
		t.Errorf("UpdatedAt changed: %v -> %v", originalPR.UpdatedAt, enriched.UpdatedAt)
	}
}

// TestFormatDigestMessage_EmptyPRLists verifies handling of empty incoming/outgoing lists.
func TestFormatDigestMessage_EmptyPRLists(t *testing.T) {
	scheduler := &DailyDigestScheduler{}

	testTime := time.Date(2025, 1, 15, 8, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		incoming []home.PR
		outgoing []home.PR
	}{
		{
			name:     "both empty",
			incoming: nil,
			outgoing: nil,
		},
		{
			name:     "incoming empty",
			incoming: nil,
			outgoing: []home.PR{{Title: "Test", URL: "https://github.com/test/repo/pull/1", ActionKind: "merge"}},
		},
		{
			name:     "outgoing empty",
			incoming: []home.PR{{Title: "Test", URL: "https://github.com/test/repo/pull/1", ActionKind: "review"}},
			outgoing: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := scheduler.formatDigestMessageAt(tt.incoming, tt.outgoing, testTime)

			// Should always have greeting and footer
			if !strings.Contains(message, "*") {
				t.Error("expected greeting")
			}
			if !strings.Contains(message, "Ready to Review") {
				t.Error("expected footer")
			}

			// Should not crash
			if message == "" {
				t.Error("message should not be empty")
			}
		})
	}
}

// TestCheckAndSend_NoOrgs tests when there are no organizations configured.
func TestCheckAndSend_NoOrgs(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{
		allOrgsFunc: func() []string {
			return []string{} // No orgs
		},
	}

	scheduler := &DailyDigestScheduler{
		githubManager: mockGitHubMgr,
		configManager: &mockConfigProvider{},
		stateStore:    &mockStateProvider{},
		slackManager:  &mockSlackManagerWithClient{},
	}

	ctx := context.Background()

	// Should not crash
	scheduler.CheckAndSend(ctx)
}

// TestCheckAndSend_DailyRemindersDisabled tests when daily reminders are disabled.
func TestCheckAndSend_DailyRemindersDisabled(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{
		allOrgsFunc: func() []string {
			return []string{"test-org"}
		},
	}

	mockConfigMgr := &mockConfigProvider{
		dailyRemindersEnabledFunc: func(org string) bool {
			return false // Disabled
		},
	}

	scheduler := &DailyDigestScheduler{
		githubManager: mockGitHubMgr,
		configManager: mockConfigMgr,
		stateStore:    &mockStateProvider{},
		slackManager:  &mockSlackManagerWithClient{},
	}

	ctx := context.Background()

	// Should not crash and should skip processing
	scheduler.CheckAndSend(ctx)
}

// TestNewDailyDigestScheduler_WithInterfaces tests scheduler creation with interfaces.
func TestNewDailyDigestScheduler_WithInterfaces(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{}
	mockConfigMgr := &mockConfigProvider{}
	mockState := &mockStateProvider{}
	mockSlack := &mockSlackManagerWithClient{}
	manager := New(mockSlack, mockConfigMgr, &mockStore{})

	scheduler := NewDailyDigestScheduler(manager, mockGitHubMgr, mockConfigMgr, mockState, mockSlack)

	if scheduler == nil {
		t.Fatal("expected non-nil scheduler")
	}

	if scheduler.githubManager != mockGitHubMgr {
		t.Error("expected github manager to be set")
	}
}
