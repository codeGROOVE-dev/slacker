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
func BuildBlocks(dashboard *Dashboard, primaryOrg string) []slack.Block {
	var blocks []slack.Block

	// Header - gradient-inspired title
	blocks = append(blocks,
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "🚀 Ready to Review", true, false),
		),
	)

	c := dashboard.Counts()

	// Status overview - quick summary
	emoji := "✨"
	status := "All clear"
	if c.IncomingBlocked > 0 || c.OutgoingBlocked > 0 {
		emoji = "⚡"
		status = "Action needed"
	}

	blocks = append(blocks,
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("%s *%s* • %d incoming • %d outgoing",
					emoji,
					status,
					c.IncomingTotal,
					c.OutgoingTotal),
				false,
				false,
			),
			nil,
			nil,
		),
	)

	// Organization monitoring + last updated
	links := make([]string, 0, len(dashboard.WorkspaceOrgs))
	for _, org := range dashboard.WorkspaceOrgs {
		// URL-escape org name to prevent injection
		esc := url.PathEscape(org)
		links = append(links, fmt.Sprintf("<%s|%s>",
			fmt.Sprintf("https://github.com/%s/.codeGROOVE/blob/main/slack.yaml", esc),
			org))
	}
	now := time.Now().Format("Jan 2, 3:04pm MST")
	context := fmt.Sprintf("Monitoring: %s  •  Updated: %s",
		strings.Join(links, ", "),
		now)

	blocks = append(blocks,
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn", context, false, false),
		),
		// Refresh button
		slack.NewActionBlock(
			"refresh_actions",
			slack.NewButtonBlockElement(
				"refresh_dashboard",
				"refresh",
				slack.NewTextBlockObject("plain_text", "🔄 Refresh Dashboard", true, false),
			).WithStyle("primary"),
		),
		slack.NewDividerBlock(),
	)

	// Use the same clean report format for PR sections
	blocks = append(blocks, BuildPRSections(dashboard.IncomingPRs, dashboard.OutgoingPRs)...)

	// Footer - full dashboard link
	// URL-escape org name to prevent injection
	esc := url.PathEscape(primaryOrg)
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("📊 <%s|View full dashboard at %s.ready-to-review.dev>",
					fmt.Sprintf("https://%s.ready-to-review.dev", esc),
					primaryOrg,
				),
				false,
				false,
			),
		),
	)

	return blocks
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

			// Format PR line - extract repo name
			repo := prs[i].Repository
			if idx := strings.LastIndex(repo, "/"); idx >= 0 {
				repo = repo[idx+1:]
			}

			// Determine indicator
			var indicator string
			switch {
			case prs[i].NeedsReview:
				indicator = ":red_circle:"
			case prs[i].IsBlocked:
				indicator = ":red_circle:"
			case prs[i].ActionKind != "":
				indicator = ":speech_balloon:"
			default:
				indicator = ":white_small_square:"
			}

			// Build line
			line := fmt.Sprintf("%s <%s|%s#%d> • %s", indicator, prs[i].URL, repo, prs[i].Number, prs[i].Title)
			if prs[i].ActionKind != "" {
				line = fmt.Sprintf("%s — %s", line, strings.ReplaceAll(prs[i].ActionKind, "_", " "))
			}
			lines = append(lines, line)
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

			// Format PR line - extract repo name
			repo := prs[i].Repository
			if idx := strings.LastIndex(repo, "/"); idx >= 0 {
				repo = repo[idx+1:]
			}

			// Determine indicator
			var indicator string
			switch {
			case prs[i].NeedsReview:
				indicator = ":green_circle:"
			case prs[i].IsBlocked:
				indicator = ":green_circle:"
			case prs[i].ActionKind != "":
				indicator = ":speech_balloon:"
			default:
				indicator = ":white_small_square:"
			}

			// Build line
			line := fmt.Sprintf("%s <%s|%s#%d> • %s", indicator, prs[i].URL, repo, prs[i].Number, prs[i].Title)
			if prs[i].ActionKind != "" {
				line = fmt.Sprintf("%s — %s", line, strings.ReplaceAll(prs[i].ActionKind, "_", " "))
			}
			lines = append(lines, line)
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

// BuildBlocksWithDebug creates Slack Block Kit UI with debug information about user mapping.
func BuildBlocksWithDebug(dashboard *Dashboard, primaryOrg string, mapping *usermapping.ReverseMapping) []slack.Block {
	// Build standard blocks first
	blocks := BuildBlocks(dashboard, primaryOrg)

	// Add debug section if mapping info is available
	if mapping != nil {
		blocks = append(blocks,
			slack.NewDividerBlock(),
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn",
					fmt.Sprintf("🔍 *Debug Info*\n"+
						"GitHub: `@%s`  •  Mapped via: `%s`  •  Confidence: `%d%%`",
						mapping.GitHubUsername,
						mapping.MatchMethod,
						mapping.Confidence),
					false,
					false,
				),
				nil,
				nil,
			),
		)

		// Add mapping guidance if confidence is low
		if mapping.Confidence < 80 {
			blocks = append(blocks,
				slack.NewContextBlock("",
					slack.NewTextBlockObject("mrkdwn",
						fmt.Sprintf("⚠️  Low confidence mapping. Add manual override to `slack.yaml`:\n```yaml\nusers:\n  %s: %s\n```",
							mapping.GitHubUsername,
							mapping.SlackEmail),
						false,
						false,
					),
				),
			)
		}
	} else {
		// No mapping found - show error message
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

	return blocks
}
