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

				// Should have PR with large red square (incoming blocked indicator)
				foundBlockedPR := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, ":large_red_square:") {
							foundBlockedPR = true
						}
					}
				}
				if !foundBlockedPR {
					t.Error("expected PR with :large_red_square: indicating blocked incoming PR")
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

				// Should have PR with large green square (outgoing blocked indicator)
				foundBlocked := false
				for _, block := range blocks {
					if sb, ok := block.(*slack.SectionBlock); ok {
						if sb.Text != nil && strings.Contains(sb.Text.Text, ":large_green_square:") {
							foundBlocked = true
						}
					}
				}
				if !foundBlocked {
					t.Error("expected PR with :large_green_square: for blocked outgoing PR")
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

// TestBuildPRSections_SortOrder verifies blocked PRs appear first, then by recency.
func TestBuildPRSections_SortOrder(t *testing.T) {
	baseTime := time.Now()

	// Create incoming PRs with mixed blocked/unblocked and timestamps
	incoming := []PR{
		{Number: 1, Title: "Oldest non-blocked", Repository: "org/repo", URL: "https://github.com/org/repo/pull/1", UpdatedAt: baseTime.Add(-4 * time.Hour), NeedsReview: false},
		{Number: 2, Title: "Newest blocked", Repository: "org/repo", URL: "https://github.com/org/repo/pull/2", UpdatedAt: baseTime.Add(-1 * time.Hour), NeedsReview: true},
		{Number: 3, Title: "Middle non-blocked", Repository: "org/repo", URL: "https://github.com/org/repo/pull/3", UpdatedAt: baseTime.Add(-2 * time.Hour), NeedsReview: false},
		{Number: 4, Title: "Oldest blocked", Repository: "org/repo", URL: "https://github.com/org/repo/pull/4", UpdatedAt: baseTime.Add(-5 * time.Hour), NeedsReview: true},
	}

	// Create outgoing PRs with mixed blocked/unblocked and timestamps
	outgoing := []PR{
		{Number: 5, Title: "Middle non-blocked out", Repository: "org/repo", URL: "https://github.com/org/repo/pull/5", UpdatedAt: baseTime.Add(-3 * time.Hour), IsBlocked: false},
		{Number: 6, Title: "Newest blocked out", Repository: "org/repo", URL: "https://github.com/org/repo/pull/6", UpdatedAt: baseTime.Add(-1 * time.Hour), IsBlocked: true},
		{Number: 7, Title: "Oldest blocked out", Repository: "org/repo", URL: "https://github.com/org/repo/pull/7", UpdatedAt: baseTime.Add(-6 * time.Hour), IsBlocked: true},
		{Number: 8, Title: "Newest non-blocked out", Repository: "org/repo", URL: "https://github.com/org/repo/pull/8", UpdatedAt: baseTime.Add(-2 * time.Hour), IsBlocked: false},
	}

	blocks := BuildPRSections(incoming, outgoing)

	// Should have 2 blocks (incoming and outgoing sections)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	// Check incoming section order
	incomingBlock, ok := blocks[0].(*slack.SectionBlock)
	if !ok {
		t.Fatal("expected first block to be SectionBlock")
	}
	incomingText := incomingBlock.Text.Text

	// Verify blocked PRs appear before non-blocked
	// Expected order: PR#2 (newest blocked), PR#4 (oldest blocked), PR#3 (middle non-blocked), PR#1 (oldest non-blocked)
	idx2 := strings.Index(incomingText, "repo#2")
	idx4 := strings.Index(incomingText, "repo#4")
	idx3 := strings.Index(incomingText, "repo#3")
	idx1 := strings.Index(incomingText, "repo#1")

	if idx2 < 0 || idx4 < 0 || idx3 < 0 || idx1 < 0 {
		t.Fatal("not all incoming PRs found in output")
	}

	// Blocked PRs (2, 4) should come before non-blocked PRs (3, 1)
	if idx2 > idx3 || idx2 > idx1 {
		t.Error("blocked PR#2 should appear before non-blocked PRs")
	}
	if idx4 > idx3 || idx4 > idx1 {
		t.Error("blocked PR#4 should appear before non-blocked PRs")
	}

	// Within blocked group: PR#2 (newer) should come before PR#4 (older)
	if idx2 > idx4 {
		t.Error("newer blocked PR#2 should appear before older blocked PR#4")
	}

	// Within non-blocked group: PR#3 (newer) should come before PR#1 (older)
	if idx3 > idx1 {
		t.Error("newer non-blocked PR#3 should appear before older non-blocked PR#1")
	}

	// Check outgoing section order
	outgoingBlock, ok := blocks[1].(*slack.SectionBlock)
	if !ok {
		t.Fatal("expected second block to be SectionBlock")
	}
	outgoingText := outgoingBlock.Text.Text

	// Expected order: PR#6 (newest blocked), PR#7 (oldest blocked), PR#8 (newest non-blocked), PR#5 (middle non-blocked)
	idx6 := strings.Index(outgoingText, "repo#6")
	idx7 := strings.Index(outgoingText, "repo#7")
	idx8 := strings.Index(outgoingText, "repo#8")
	idx5 := strings.Index(outgoingText, "repo#5")

	if idx6 < 0 || idx7 < 0 || idx8 < 0 || idx5 < 0 {
		t.Fatal("not all outgoing PRs found in output")
	}

	// Blocked PRs (6, 7) should come before non-blocked PRs (8, 5)
	if idx6 > idx8 || idx6 > idx5 {
		t.Error("blocked PR#6 should appear before non-blocked PRs")
	}
	if idx7 > idx8 || idx7 > idx5 {
		t.Error("blocked PR#7 should appear before non-blocked PRs")
	}

	// Within blocked group: PR#6 (newer) should come before PR#7 (older)
	if idx6 > idx7 {
		t.Error("newer blocked PR#6 should appear before older blocked PR#7")
	}

	// Within non-blocked group: PR#8 (newer) should come before PR#5 (older)
	if idx8 > idx5 {
		t.Error("newer non-blocked PR#8 should appear before older non-blocked PR#5")
	}

	// Verify color indicators
	if !strings.Contains(incomingText, ":large_red_square:") {
		t.Error("incoming blocked PRs should use :large_red_square:")
	}
	if !strings.Contains(outgoingText, ":large_green_square:") {
		t.Error("outgoing blocked PRs should use :large_green_square:")
	}
}
