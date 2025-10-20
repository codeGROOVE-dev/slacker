package home

import (
	"fmt"
	"net/url"
	"strings"
	"time"

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

	counts := dashboard.Counts()

	// Status overview - quick summary
	statusEmoji := "✨"
	statusText := "All clear"
	if counts.IncomingBlocked > 0 || counts.OutgoingBlocked > 0 {
		statusEmoji = "⚡"
		statusText = "Action needed"
	}

	blocks = append(blocks,
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("%s *%s* • %d incoming • %d outgoing",
					statusEmoji,
					statusText,
					counts.IncomingTotal,
					counts.OutgoingTotal),
				false,
				false,
			),
			nil,
			nil,
		),
	)

	// Organization monitoring + last updated
	orgLinks := make([]string, 0, len(dashboard.WorkspaceOrgs))
	for _, org := range dashboard.WorkspaceOrgs {
		// URL-escape org name to prevent injection
		escaped := url.PathEscape(org)
		orgLinks = append(orgLinks, fmt.Sprintf("<%s|%s>",
			fmt.Sprintf("https://github.com/%s/.codeGROOVE/blob/main/slack.yaml", escaped),
			org))
	}
	updated := time.Now().Format("Jan 2, 3:04pm MST")
	ctx := fmt.Sprintf("Monitoring: %s  •  Updated: %s",
		strings.Join(orgLinks, ", "),
		updated)

	blocks = append(blocks,
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn", ctx, false, false),
		),
	)

	// Refresh button
	blocks = append(blocks,
		slack.NewActionBlock(
			"refresh_actions",
			slack.NewButtonBlockElement(
				"refresh_dashboard",
				"refresh",
				slack.NewTextBlockObject("plain_text", "🔄 Refresh Dashboard", true, false),
			).WithStyle("primary"),
		),
	)

	// Incoming PRs section
	blocks = append(blocks, slack.NewDividerBlock())

	incoming := fmt.Sprintf(":arrow_down: *Incoming PRs* (%d total)", counts.IncomingTotal)
	if counts.IncomingBlocked > 0 {
		incoming = fmt.Sprintf(":rotating_light: *Incoming PRs* • *%d blocked on you* • %d total", counts.IncomingBlocked, counts.IncomingTotal)
	}

	blocks = append(blocks,
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", incoming, false, false),
			nil,
			nil,
		),
	)

	if len(dashboard.IncomingPRs) == 0 {
		blocks = append(blocks,
			slack.NewContextBlock("",
				slack.NewTextBlockObject("mrkdwn", "No incoming PRs • You're all caught up!", false, false),
			),
		)
	} else {
		for i := range dashboard.IncomingPRs {
			blocks = append(blocks, formatEnhancedPRBlock(&dashboard.IncomingPRs[i]))
		}
	}

	// Outgoing PRs section
	blocks = append(blocks, slack.NewDividerBlock())

	outgoing := fmt.Sprintf(":arrow_up: *Outgoing PRs* (%d total)", counts.OutgoingTotal)
	if counts.OutgoingBlocked > 0 {
		outgoing = fmt.Sprintf(":hourglass_flowing_sand: *Outgoing PRs* • *%d waiting* • %d total", counts.OutgoingBlocked, counts.OutgoingTotal)
	}

	blocks = append(blocks,
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", outgoing, false, false),
			nil,
			nil,
		),
	)

	if len(dashboard.OutgoingPRs) == 0 {
		blocks = append(blocks,
			slack.NewContextBlock("",
				slack.NewTextBlockObject("mrkdwn", "No outgoing PRs • Time to ship something new!", false, false),
			),
		)
	} else {
		for i := range dashboard.OutgoingPRs {
			blocks = append(blocks, formatEnhancedPRBlock(&dashboard.OutgoingPRs[i]))
		}
	}

	// Footer - full dashboard link
	// URL-escape org name to prevent injection
	escapedOrg := url.PathEscape(primaryOrg)
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewContextBlock("",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("📊 <%s|View full dashboard at %s.ready-to-review.dev>",
					fmt.Sprintf("https://%s.ready-to-review.dev", escapedOrg),
					primaryOrg,
				),
				false,
				false,
			),
		),
	)

	return blocks
}

// formatEnhancedPRBlock formats a single PR with enhanced visual design.
// Inspired by dash.ready-to-review.dev with more informative, actionable display.
func formatEnhancedPRBlock(pr *PR) slack.Block {
	// Status indicators - clear visual hierarchy
	var emoji, status string
	if pr.IsBlocked {
		if pr.NeedsReview {
			// Blocked on YOU - highest priority
			emoji = "🚨"
			status = "*BLOCKED ON YOU*"
		} else {
			// Blocked on author
			emoji = "⏸️"
			status = "Blocked on author"
		}
	} else if pr.NeedsReview {
		// Ready for your review
		emoji = "👀"
		status = "Ready for review"
	} else {
		// Waiting/in progress
		emoji = "⏳"
		status = "In progress"
	}

	// Extract repo name
	parts := strings.SplitN(pr.Repository, "/", 2)
	repo := pr.Repository
	if len(parts) == 2 {
		repo = parts[1]
	}
	ref := fmt.Sprintf("%s#%d", repo, pr.Number)

	// Build main line with status
	line := fmt.Sprintf("%s <%s|*%s*>  •  %s", emoji, pr.URL, ref, status)

	// Add action kind if present
	if pr.ActionKind != "" {
		action := strings.ReplaceAll(pr.ActionKind, "_", " ")
		line = fmt.Sprintf("%s  •  %s", line, action)
	}

	// Add age indicator
	// Inline formatAge since it's only called once (simplicity)
	age := time.Since(pr.UpdatedAt)
	var ageStr string
	switch {
	case age < time.Hour:
		ageStr = fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		ageStr = fmt.Sprintf("%dh", int(age.Hours()))
	case age < 30*24*time.Hour:
		ageStr = fmt.Sprintf("%dd", int(age.Hours()/24))
	case age < 365*24*time.Hour:
		ageStr = fmt.Sprintf("%dmo", int(age.Hours()/(24*30)))
	default:
		ageStr = pr.UpdatedAt.Format("2006")
	}
	line = fmt.Sprintf("%s  •  _updated %s ago_", line, ageStr)

	// Title on second line (truncated if needed)
	// Use rune slicing to safely handle multi-byte UTF-8 characters
	title := pr.Title
	runes := []rune(title)
	if len(runes) > 120 {
		title = string(runes[:117]) + "..."
	}

	text := fmt.Sprintf("%s\n%s", line, title)

	return slack.NewSectionBlock(
		slack.NewTextBlockObject("mrkdwn", text, false, false),
		nil,
		nil,
	)
}
