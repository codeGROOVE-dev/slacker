package home

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
	"github.com/slack-go/slack"
)

// BuildBlocks creates Slack Block Kit UI for the home dashboard.
// Design matches dashboard at https://ready-to-review.dev - modern minimal with indigo accents.
func BuildBlocks(dashboard *Dashboard, userTZ string) []slack.Block {
	var blocks []slack.Block

	// Header
	blocks = append(blocks,
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "🚀 Ready to Review", true, false),
		),
		// Refresh button
		slack.NewActionBlock(
			"refresh_actions",
			slack.NewButtonBlockElement(
				"refresh_dashboard",
				"refresh",
				slack.NewTextBlockObject("plain_text", "🔄 Refresh", true, false),
			).WithStyle("primary"),
		),
	)

	// Updated timestamp - right after refresh button
	now := formatTimestamp(time.Now(), userTZ)
	blocks = append(blocks,
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("Updated %s", now),
				false,
				false,
			),
		),
		slack.NewDividerBlock(),
	)

	// PR sections
	blocks = append(blocks, BuildPRSections(dashboard.IncomingPRs, dashboard.OutgoingPRs)...)

	// Organizations section - only show if there are orgs configured
	if len(dashboard.WorkspaceOrgs) > 0 {
		blocks = append(blocks, slack.NewDividerBlock())

		// Sort orgs alphabetically
		sortedOrgs := make([]string, len(dashboard.WorkspaceOrgs))
		copy(sortedOrgs, dashboard.WorkspaceOrgs)
		sort.Strings(sortedOrgs)

		var orgLines []string
		for _, org := range sortedOrgs {
			// URL-escape org name to prevent injection
			esc := url.PathEscape(org)
			orgLine := fmt.Sprintf("• %s [<%s|dashboard> | <%s|config>]",
				org,
				fmt.Sprintf("https://%s.ready-to-review.dev", esc),
				fmt.Sprintf("https://github.com/%s/.codeGROOVE/blob/main/slack.yaml", esc),
			)
			orgLines = append(orgLines, orgLine)
		}

		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn",
					"*Organizations*\n"+strings.Join(orgLines, "\n"),
					false,
					false,
				),
				nil,
				nil,
			),
		)
	}

	return blocks
}

// formatPRLine formats a single PR line with indicator, repo, number, title, and optional action.
func formatPRLine(pr PR, isIncoming bool) string {
	// Extract repo name
	repo := pr.Repository
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		repo = repo[idx+1:]
	}

	// Determine indicator
	var indicator string
	if isIncoming {
		switch {
		case pr.NeedsReview, pr.IsBlocked:
			indicator = ":red_circle:"
		case pr.ActionKind != "":
			indicator = ":speech_balloon:"
		default:
			indicator = ":white_small_square:"
		}
	} else {
		switch {
		case pr.NeedsReview, pr.IsBlocked:
			indicator = ":large_green_circle:"
		case pr.ActionKind != "":
			indicator = ":speech_balloon:"
		default:
			indicator = ":white_small_square:"
		}
	}

	// Build line
	line := fmt.Sprintf("%s <%s|%s#%d> • %s", indicator, pr.URL, repo, pr.Number, pr.Title)
	if pr.ActionKind != "" {
		action := strings.ReplaceAll(pr.ActionKind, "_", " ")
		// Bold the action if this PR is blocked on the user
		if pr.IsBlocked || (isIncoming && pr.NeedsReview) {
			action = "*" + action + "*"
		}
		line = fmt.Sprintf("%s — %s", line, action)
	}
	return line
}

// BuildPRSections creates Block Kit blocks for PR sections (incoming/outgoing).
// Format inspired by goose - simple, minimal, action-focused.
// This is the core formatting used by both daily reports and the dashboard.
func BuildPRSections(incoming, outgoing []PR) []slack.Block {
	var blocks []slack.Block

	// Incoming PRs section
	if len(incoming) > 0 {
		// Sort: blocked first, then by most recent within each group
		prs := make([]PR, len(incoming))
		copy(prs, incoming)
		sort.Slice(prs, func(i, j int) bool {
			iBlocked := prs[i].IsBlocked || prs[i].NeedsReview
			jBlocked := prs[j].IsBlocked || prs[j].NeedsReview
			if iBlocked != jBlocked {
				return iBlocked // blocked items first
			}
			return prs[i].UpdatedAt.After(prs[j].UpdatedAt)
		})

		// Count blocked PRs and format lines
		n := 0
		var lines []string
		for i := range prs {
			if prs[i].IsBlocked || prs[i].NeedsReview {
				n++
			}
			lines = append(lines, formatPRLine(prs[i], true))
		}

		// Build header
		h := "*Incoming*"
		if n > 0 {
			if n == 1 {
				h = "*Incoming — 1 blocked on you*"
			} else {
				h = fmt.Sprintf("*Incoming — %d blocked on you*", n)
			}
		}

		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", h+"\n\n"+strings.Join(lines, "\n"), false, false),
				nil, nil,
			),
		)
	}

	// Spacer between Incoming and Outgoing sections
	if len(incoming) > 0 && len(outgoing) > 0 {
		blocks = append(blocks,
			slack.NewContextBlock("",
				slack.NewTextBlockObject("plain_text", " ", false, false),
			),
		)
	}

	// Outgoing PRs section
	if len(outgoing) > 0 {
		// Sort: blocked first, then by most recent within each group
		prs := make([]PR, len(outgoing))
		copy(prs, outgoing)
		sort.Slice(prs, func(i, j int) bool {
			iBlocked := prs[i].IsBlocked
			jBlocked := prs[j].IsBlocked
			if iBlocked != jBlocked {
				return iBlocked // blocked items first
			}
			return prs[i].UpdatedAt.After(prs[j].UpdatedAt)
		})

		// Count blocked PRs and format lines
		n := 0
		var lines []string
		for i := range prs {
			if prs[i].IsBlocked {
				n++
			}
			lines = append(lines, formatPRLine(prs[i], false))
		}

		// Build header
		h := "*Outgoing*"
		if n > 0 {
			if n == 1 {
				h = "*Outgoing — 1 blocked on you*"
			} else {
				h = fmt.Sprintf("*Outgoing — %d blocked on you*", n)
			}
		}

		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", h+"\n\n"+strings.Join(lines, "\n"), false, false),
				nil, nil,
			),
		)
	}

	return blocks
}

// BuildBlocksWithDebug creates Slack Block Kit UI with GitHub username integrated into the header.
func BuildBlocksWithDebug(dashboard *Dashboard, userTZ string, mapping *usermapping.ReverseMapping) []slack.Block {
	var blocks []slack.Block

	// Header with GitHub username if available
	headerText := "🚀 Ready to Review"
	if mapping != nil {
		headerText = fmt.Sprintf("🚀 Ready to Review — @%s", mapping.GitHubUsername)
	}

	blocks = append(blocks,
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", headerText, true, false),
		),
		// Refresh button
		slack.NewActionBlock(
			"refresh_actions",
			slack.NewButtonBlockElement(
				"refresh_dashboard",
				"refresh",
				slack.NewTextBlockObject("plain_text", "🔄 Refresh", true, false),
			).WithStyle("primary"),
		),
		slack.NewDividerBlock(),
	)

	// PR sections
	blocks = append(blocks, BuildPRSections(dashboard.IncomingPRs, dashboard.OutgoingPRs)...)

	// Add spacing after PR sections if we have content
	if len(dashboard.IncomingPRs) > 0 || len(dashboard.OutgoingPRs) > 0 {
		blocks = append(blocks,
			slack.NewContextBlock("",
				slack.NewTextBlockObject("plain_text", " ", false, false),
			),
		)
	}

	// Organizations section - only show if there are orgs configured
	if len(dashboard.WorkspaceOrgs) > 0 {
		blocks = append(blocks, slack.NewDividerBlock())

		// Sort orgs alphabetically
		sortedOrgs := make([]string, len(dashboard.WorkspaceOrgs))
		copy(sortedOrgs, dashboard.WorkspaceOrgs)
		sort.Strings(sortedOrgs)

		var orgLines []string
		for _, org := range sortedOrgs {
			// URL-escape org name to prevent injection
			esc := url.PathEscape(org)
			orgLine := fmt.Sprintf("• %s [<%s|dashboard> | <%s|config>]",
				org,
				fmt.Sprintf("https://%s.ready-to-review.dev", esc),
				fmt.Sprintf("https://github.com/%s/.codeGROOVE/blob/main/slack.yaml", esc),
			)
			orgLines = append(orgLines, orgLine)
		}

		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn",
					"*Organizations*\n"+strings.Join(orgLines, "\n"),
					false,
					false,
				),
				nil,
				nil,
			),
		)
	}

	// Add helpful messages for error cases
	if mapping == nil && len(dashboard.WorkspaceOrgs) == 0 {
		// No organizations configured for this workspace (likely startup/race condition)
		blocks = append(blocks,
			slack.NewDividerBlock(),
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn",
					"⏳ *Setting up workspace...*\n"+
						"No organizations configured yet. This usually resolves automatically.\n\n"+
						"If this persists:\n"+
						"1. Ensure your GitHub App is installed for your organization\n"+
						"2. Check that `.codeGROOVE/slack.yaml` exists in your org's `.github` repo\n"+
						"3. Verify the `slack:` field matches this workspace's domain",
					false,
					false,
				),
				nil,
				nil,
			),
		)
	} else if mapping == nil {
		// User mapping failed
		blocks = append(blocks,
			slack.NewDividerBlock(),
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn",
					"❌ *Could not map Slack user to GitHub*\n"+
						"Add your mapping to `.codeGROOVE/slack.yaml`:\n```yaml\nusers:\n  your-github-username: your-email@company.com\n```",
					false,
					false,
				),
				nil,
				nil,
			),
		)
	}

	// Updated timestamp at the bottom
	now := formatTimestamp(time.Now(), userTZ)
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("Updated %s", now),
				false,
				false,
			),
		),
	)

	return blocks
}

// formatTimestamp formats a timestamp in the user's timezone without the colon after "Updated".
// Example: "Nov 6, 12:48am America/Los_Angeles".
func formatTimestamp(t time.Time, tzName string) string {
	// Load the timezone
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		// Fallback to UTC if timezone is invalid
		loc = time.UTC
	}

	// Convert to user's timezone
	t = t.In(loc)

	// Format as "Jan 2, 3:04pm MST"
	return t.Format("Jan 2, 3:04pm MST")
}
