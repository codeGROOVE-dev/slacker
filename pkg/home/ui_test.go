package home

import (
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// TestBuildBlocks verifies home dashboard block generation.
func TestBuildBlocks(t *testing.T) {
	tests := []struct {
		name       string
		dashboard  *Dashboard
		primaryOrg string
		validate   func(t *testing.T, blocks []slack.Block)
	}{
		{
			name: "empty dashboard",
			dashboard: &Dashboard{
				WorkspaceOrgs: []string{"test-org"},
				IncomingPRs:   []PR{},
				OutgoingPRs:   []PR{},
			},
			primaryOrg: "test-org",
			validate: func(t *testing.T, blocks []slack.Block) {
				if len(blocks) == 0 {
					t.Fatal("expected non-empty blocks")
				}

				// Should have header
				foundHeader := false
				for _, block := range blocks {
					if hb, ok := block.(*slack.HeaderBlock); ok {
						if strings.Contains(hb.Text.Text, "Ready to Review") {
							foundHeader = true
						}
					}
				}
				if !foundHeader {
					t.Error("expected header block with 'Ready to Review'")
				}

				// Should have "All clear" status
				foundStatus := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "All clear") {
							foundStatus = true
						}
					}
				}
				if !foundStatus {
					t.Error("expected 'All clear' status for empty dashboard")
				}

				// Should have "No incoming PRs" message
				foundIncoming := false
				for _, block := range blocks {
					if cb, ok := block.(*slack.ContextBlock); ok {
						for _, elem := range cb.ContextElements.Elements {
							if txt, ok := elem.(*slack.TextBlockObject); ok {
								if strings.Contains(txt.Text, "No incoming PRs") {
									foundIncoming = true
								}
							}
						}
					}
				}
				if !foundIncoming {
					t.Error("expected 'No incoming PRs' message")
				}

				// Should have dashboard link
				foundLink := false
				for _, block := range blocks {
					if cb, ok := block.(*slack.ContextBlock); ok {
						for _, elem := range cb.ContextElements.Elements {
							if txt, ok := elem.(*slack.TextBlockObject); ok {
								if strings.Contains(txt.Text, "ready-to-review.dev") {
									foundLink = true
								}
							}
						}
					}
				}
				if !foundLink {
					t.Error("expected dashboard link in footer")
				}
			},
		},
		{
			name: "dashboard with blocked incoming PRs",
			dashboard: &Dashboard{
				WorkspaceOrgs: []string{"test-org"},
				IncomingPRs: []PR{
					{
						Number:      123,
						Title:       "Fix critical bug",
						Author:      "alice",
						Repository:  "test-org/repo",
						URL:         "https://github.com/test-org/repo/pull/123",
						ActionKind:  "review",
						IsBlocked:   true,
						NeedsReview: true,
						UpdatedAt:   time.Now().Add(-2 * time.Hour),
					},
				},
				OutgoingPRs: []PR{},
			},
			primaryOrg: "test-org",
			validate: func(t *testing.T, blocks []slack.Block) {
				// Should have "Action needed" status
				foundActionNeeded := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "Action needed") {
							foundActionNeeded = true
						}
					}
				}
				if !foundActionNeeded {
					t.Error("expected 'Action needed' status with blocked PRs")
				}

				// Should show "1 blocked on you"
				foundBlocked := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "1 blocked on you") {
							foundBlocked = true
						}
					}
				}
				if !foundBlocked {
					t.Error("expected 'blocked on you' message")
				}

				// Should have PR with "BLOCKED ON YOU" status
				foundBlockedPR := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "BLOCKED ON YOU") {
							foundBlockedPR = true
						}
					}
				}
				if !foundBlockedPR {
					t.Error("expected PR block with 'BLOCKED ON YOU' status")
				}
			},
		},
		{
			name: "dashboard with outgoing PRs",
			dashboard: &Dashboard{
				WorkspaceOrgs: []string{"test-org"},
				IncomingPRs:   []PR{},
				OutgoingPRs: []PR{
					{
						Number:     456,
						Title:      "Add new feature",
						Author:     "me",
						Repository: "test-org/repo",
						URL:        "https://github.com/test-org/repo/pull/456",
						ActionKind: "address_feedback",
						IsBlocked:  true,
						UpdatedAt:  time.Now().Add(-1 * time.Hour),
					},
				},
			},
			primaryOrg: "test-org",
			validate: func(t *testing.T, blocks []slack.Block) {
				// Should show outgoing PR section
				foundOutgoing := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "Outgoing PRs") {
							foundOutgoing = true
						}
					}
				}
				if !foundOutgoing {
					t.Error("expected 'Outgoing PRs' section")
				}

				// Should show waiting count
				foundWaiting := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "1 waiting") {
							foundWaiting = true
						}
					}
				}
				if !foundWaiting {
					t.Error("expected 'waiting' count for blocked outgoing PRs")
				}
			},
		},
		{
			name: "dashboard with multiple orgs",
			dashboard: &Dashboard{
				WorkspaceOrgs: []string{"org1", "org2", "org3"},
				IncomingPRs:   []PR{},
				OutgoingPRs:   []PR{},
			},
			primaryOrg: "org1",
			validate: func(t *testing.T, blocks []slack.Block) {
				// Should list all orgs in monitoring section
				foundOrgs := 0
				for _, block := range blocks {
					if cb, ok := block.(*slack.ContextBlock); ok {
						for _, elem := range cb.ContextElements.Elements {
							if txt, ok := elem.(*slack.TextBlockObject); ok {
								if strings.Contains(txt.Text, "org1") {
									foundOrgs++
								}
								if strings.Contains(txt.Text, "org2") {
									foundOrgs++
								}
								if strings.Contains(txt.Text, "org3") {
									foundOrgs++
								}
							}
						}
					}
				}
				if foundOrgs < 3 {
					t.Errorf("expected all 3 orgs in monitoring section, found %d", foundOrgs)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := BuildBlocks(tt.dashboard, tt.primaryOrg)
			tt.validate(t, blocks)
		})
	}
}

// TestFormatEnhancedPRBlock verifies individual PR block formatting.
func TestFormatEnhancedPRBlock(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		pr       *PR
		validate func(t *testing.T, block slack.Block)
	}{
		{
			name: "blocked on you - highest priority",
			pr: &PR{
				Number:      123,
				Title:       "Fix authentication",
				Repository:  "org/repo",
				URL:         "https://github.com/org/repo/pull/123",
				IsBlocked:   true,
				NeedsReview: true,
				UpdatedAt:   now.Add(-2 * time.Hour),
			},
			validate: func(t *testing.T, block slack.Block) {
				sb, ok := block.(*slack.SectionBlock)
				if !ok {
					t.Fatal("expected SectionBlock")
				}

				text := sb.Text.Text

				// Should have urgent emoji
				if !strings.Contains(text, "🚨") {
					t.Error("expected 🚨 emoji for blocked on you")
				}

				// Should have "BLOCKED ON YOU" status
				if !strings.Contains(text, "BLOCKED ON YOU") {
					t.Error("expected 'BLOCKED ON YOU' status")
				}

				// Should have repo#number format
				if !strings.Contains(text, "repo#123") {
					t.Error("expected 'repo#123' reference")
				}

				// Should have age indicator
				if !strings.Contains(text, "2h ago") {
					t.Error("expected '2h ago' age indicator")
				}

				// Should have title
				if !strings.Contains(text, "Fix authentication") {
					t.Error("expected PR title")
				}
			},
		},
		{
			name: "blocked on author",
			pr: &PR{
				Number:      456,
				Title:       "Add feature",
				Repository:  "org/repo",
				URL:         "https://github.com/org/repo/pull/456",
				IsBlocked:   true,
				NeedsReview: false,
				ActionKind:  "address_feedback",
				UpdatedAt:   now.Add(-1 * time.Hour),
			},
			validate: func(t *testing.T, block slack.Block) {
				sb := block.(*slack.SectionBlock)
				text := sb.Text.Text

				// Should have pause emoji
				if !strings.Contains(text, "⏸️") {
					t.Error("expected ⏸️ emoji for blocked on author")
				}

				// Should have "Blocked on author" status
				if !strings.Contains(text, "Blocked on author") {
					t.Error("expected 'Blocked on author' status")
				}

				// Should have action kind
				if !strings.Contains(text, "address feedback") {
					t.Error("expected action kind with underscores replaced")
				}
			},
		},
		{
			name: "ready for review",
			pr: &PR{
				Number:      789,
				Title:       "Update docs",
				Repository:  "org/repo",
				URL:         "https://github.com/org/repo/pull/789",
				IsBlocked:   false,
				NeedsReview: true,
				UpdatedAt:   now.Add(-30 * time.Minute),
			},
			validate: func(t *testing.T, block slack.Block) {
				sb := block.(*slack.SectionBlock)
				text := sb.Text.Text

				// Should have eyes emoji
				if !strings.Contains(text, "👀") {
					t.Error("expected 👀 emoji for ready for review")
				}

				// Should have "Ready for review" status
				if !strings.Contains(text, "Ready for review") {
					t.Error("expected 'Ready for review' status")
				}

				// Should show age in minutes
				if !strings.Contains(text, "30m ago") {
					t.Error("expected '30m ago' age indicator")
				}
			},
		},
		{
			name: "in progress",
			pr: &PR{
				Number:      999,
				Title:       "Work in progress",
				Repository:  "org/repo",
				URL:         "https://github.com/org/repo/pull/999",
				IsBlocked:   false,
				NeedsReview: false,
				UpdatedAt:   now.Add(-24 * time.Hour),
			},
			validate: func(t *testing.T, block slack.Block) {
				sb := block.(*slack.SectionBlock)
				text := sb.Text.Text

				// Should have hourglass emoji
				if !strings.Contains(text, "⏳") {
					t.Error("expected ⏳ emoji for in progress")
				}

				// Should have "In progress" status
				if !strings.Contains(text, "In progress") {
					t.Error("expected 'In progress' status")
				}

				// Should show age in days for exactly 24 hours (age >= 24h shows as days)
				if !strings.Contains(text, "1d ago") {
					t.Error("expected '1d ago' age indicator")
				}
			},
		},
		{
			name: "long title truncation",
			pr: &PR{
				Number:     111,
				Title:      strings.Repeat("a", 150),
				Repository: "org/repo",
				URL:        "https://github.com/org/repo/pull/111",
				UpdatedAt:  now,
			},
			validate: func(t *testing.T, block slack.Block) {
				sb := block.(*slack.SectionBlock)
				text := sb.Text.Text

				// Should truncate to 120 characters with "..."
				if !strings.Contains(text, "...") {
					t.Error("expected truncation ellipsis for long title")
				}

				// Title line should not exceed reasonable length
				lines := strings.Split(text, "\n")
				if len(lines) > 0 {
					titleLine := lines[len(lines)-1]
					if len(titleLine) > 125 {
						t.Errorf("title line too long: %d characters", len(titleLine))
					}
				}
			},
		},
		{
			name: "age formatting - days",
			pr: &PR{
				Number:     222,
				Title:      "Old PR",
				Repository: "org/repo",
				URL:        "https://github.com/org/repo/pull/222",
				UpdatedAt:  now.Add(-5 * 24 * time.Hour),
			},
			validate: func(t *testing.T, block slack.Block) {
				sb := block.(*slack.SectionBlock)
				text := sb.Text.Text

				// Should show age in days
				if !strings.Contains(text, "5d ago") {
					t.Error("expected '5d ago' age indicator")
				}
			},
		},
		{
			name: "age formatting - months",
			pr: &PR{
				Number:     333,
				Title:      "Very old PR",
				Repository: "org/repo",
				URL:        "https://github.com/org/repo/pull/333",
				UpdatedAt:  now.Add(-60 * 24 * time.Hour),
			},
			validate: func(t *testing.T, block slack.Block) {
				sb := block.(*slack.SectionBlock)
				text := sb.Text.Text

				// Should show age in months (approximately 2 months)
				if !strings.Contains(text, "mo ago") {
					t.Error("expected month age indicator")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := formatEnhancedPRBlock(tt.pr)
			tt.validate(t, block)
		})
	}
}

// TestBuildBlocks_RefreshButton verifies refresh button is present.
func TestBuildBlocks_RefreshButton(t *testing.T) {
	dashboard := &Dashboard{
		WorkspaceOrgs: []string{"test-org"},
		IncomingPRs:   []PR{},
		OutgoingPRs:   []PR{},
	}

	blocks := BuildBlocks(dashboard, "test-org")

	// Should have action block with refresh button
	foundRefresh := false
	for _, block := range blocks {
		if ab, ok := block.(*slack.ActionBlock); ok {
			for _, elem := range ab.Elements.ElementSet {
				if btn, ok := elem.(*slack.ButtonBlockElement); ok {
					if btn.ActionID == "refresh_dashboard" {
						foundRefresh = true
						// Verify it's styled as primary
						if btn.Style != "primary" {
							t.Error("expected refresh button to have primary style")
						}
					}
				}
			}
		}
	}

	if !foundRefresh {
		t.Error("expected refresh button in dashboard")
	}
}

// TestBuildBlocks_DividersBetweenSections verifies sections are properly separated.
func TestBuildBlocks_DividersBetweenSections(t *testing.T) {
	dashboard := &Dashboard{
		WorkspaceOrgs: []string{"test-org"},
		IncomingPRs: []PR{
			{Number: 1, Title: "PR 1", Repository: "org/repo", URL: "https://github.com/org/repo/pull/1", UpdatedAt: time.Now()},
		},
		OutgoingPRs: []PR{
			{Number: 2, Title: "PR 2", Repository: "org/repo", URL: "https://github.com/org/repo/pull/2", UpdatedAt: time.Now()},
		},
	}

	blocks := BuildBlocks(dashboard, "test-org")

	// Count dividers
	dividerCount := 0
	for _, block := range blocks {
		if _, ok := block.(*slack.DividerBlock); ok {
			dividerCount++
		}
	}

	// Should have at least 3 dividers (before incoming, before outgoing, before footer)
	if dividerCount < 3 {
		t.Errorf("expected at least 3 dividers, got %d", dividerCount)
	}
}
