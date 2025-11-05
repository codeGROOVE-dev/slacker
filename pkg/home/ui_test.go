package home

import (
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// TestBuildBlocks verifies home dashboard block generation.
//
//nolint:gocognit,maintidx // Comprehensive test with many test cases - complexity acceptable
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
				t.Helper()
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

				// With new format, empty dashboards don't show "No incoming PRs" message
				// They just show header/status/refresh with no PR sections
				// Verify we don't have any PR section blocks
				hasPRSections := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && (strings.Contains(sb.Text.Text, "Incoming") || strings.Contains(sb.Text.Text, "Outgoing")) {
							hasPRSections = true
						}
					}
				}
				if hasPRSections {
					t.Error("expected no PR sections for empty dashboard")
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
				t.Helper()
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

				// Should show "1 blocked on you" in section header (new format)
				foundBlocked := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "1 blocked on you") {
							foundBlocked = true
						}
					}
				}
				if !foundBlocked {
					t.Error("expected 'blocked on you' message in header")
				}

				// Should have PR with green square (incoming blocked indicator)
				foundBlockedPR := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, ":green_square:") {
							foundBlockedPR = true
						}
					}
				}
				if !foundBlockedPR {
					t.Error("expected PR with :green_square: indicating blocked incoming PR")
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
				t.Helper()
				// Should show outgoing PR section with "blocked on you" (new format)
				foundOutgoing := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, "Outgoing") {
							foundOutgoing = true
							// Should show "1 blocked on you" in the header
							if !strings.Contains(sb.Text.Text, "1 blocked on you") {
								t.Error("expected '1 blocked on you' in Outgoing section header")
							}
						}
					}
				}
				if !foundOutgoing {
					t.Error("expected 'Outgoing' section")
				}

				// Should have PR with red square (outgoing blocked indicator)
				foundBlocked := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, ":large_red_square:") {
							foundBlocked = true
						}
					}
				}
				if !foundBlocked {
					t.Error("expected PR with :large_red_square: for blocked outgoing PR")
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
				t.Helper()
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

	// With new unified format: 2 dividers (after refresh button, before footer)
	// PR sections flow together without dividers between them
	if dividerCount < 2 {
		t.Errorf("expected at least 2 dividers, got %d", dividerCount)
	}
}
