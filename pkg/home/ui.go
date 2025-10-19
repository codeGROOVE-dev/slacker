package home

import (
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

// BuildBlocks creates Slack Block Kit UI for the home dashboard.
// Design matches dashboard at https://ready-to-review.dev - modern minimal with indigo accents.
func BuildBlocks(dashboard *Dashboard, primaryOrg string) []slack.Block {
	var blocks []slack.Block

	// Header - matches dashboard's gradient header
	blocks = append(blocks,
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "🚀 Ready to Review", false, false),
		),
	)

	counts := dashboard.Counts()

	// Incoming section - matches dashboard's card-based sections
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("*🪿 Incoming PRs* • %d blocked • %d total",
					counts.IncomingBlocked,
					counts.IncomingTotal),
				false,
				false,
			),
			nil,
			nil,
		),
	)

	if len(dashboard.IncomingPRs) == 0 {
		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", "_No incoming PRs_", false, false),
				nil,
				nil,
			),
		)
	} else {
		for i := range dashboard.IncomingPRs {
			blocks = append(blocks, formatPRBlock(&dashboard.IncomingPRs[i]))
		}
	}

	// Outgoing section - matches dashboard's card-based sections
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("*:popper: Outgoing PRs* • %d blocked • %d total",
					counts.OutgoingBlocked,
					counts.OutgoingTotal),
				false,
				false,
			),
			nil,
			nil,
		),
	)

	if len(dashboard.OutgoingPRs) == 0 {
		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", "_No outgoing PRs_", false, false),
				nil,
				nil,
			),
		)
	} else {
		for i := range dashboard.OutgoingPRs {
			blocks = append(blocks, formatPRBlock(&dashboard.OutgoingPRs[i]))
		}
	}

	// Footer with link to comprehensive web dashboard - matches dashboard styling
	blocks = append(blocks,
		slack.NewDividerBlock(),
		slack.NewContextBlock(
			"",
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("✨ Visit <%s|%s.ready-to-review.dev> for the full dashboard experience",
					fmt.Sprintf("https://%s.ready-to-review.dev", primaryOrg),
					primaryOrg,
				),
				false,
				false,
			),
		),
	)

	return blocks
}

// formatPRBlock formats a single PR as a Slack block.
// Matches dashboard's card design with clean hierarchy.
func formatPRBlock(pr *PR) slack.Block {
	// Status emoji - matches dashboard's visual indicators
	// Using indigo/purple themed emojis to match dashboard color scheme
	emoji := "🔵" // normal state (matches dashboard's indigo)
	if pr.IsBlocked || pr.NeedsReview {
		emoji = "🟣" // blocked/needs attention (matches dashboard's purple accent)
	}

	// Build PR line: 🟣 repo#number — action • age
	// Extract repo name from "owner/repo"
	repoParts := strings.SplitN(pr.Repository, "/", 2)
	repo := pr.Repository
	if len(repoParts) == 2 {
		repo = repoParts[1]
	}
	prRef := fmt.Sprintf("%s#%d", repo, pr.Number)

	line := fmt.Sprintf("%s <%s|*%s*>", emoji, pr.URL, prRef)

	// Add action kind if present - matches dashboard's secondary text style
	if pr.ActionKind != "" {
		actionDisplay := strings.ReplaceAll(pr.ActionKind, "_", " ")
		line = fmt.Sprintf("%s → %s", line, actionDisplay)
	}

	// Add age - matches dashboard's tertiary text style
	age := formatAge(pr.UpdatedAt)
	line = fmt.Sprintf("%s • `%s`", line, age)

	// Title as secondary line (truncated if too long) - matches dashboard's card content
	title := pr.Title
	if len(title) > 100 {
		title = title[:97] + "..."
	}

	text := fmt.Sprintf("%s\n%s", line, title)

	return slack.NewSectionBlock(
		slack.NewTextBlockObject("mrkdwn", text, false, false),
		nil,
		nil,
	)
}

// formatAge formats a timestamp as human-readable age.
// Matches goose's format: 30m, 5h, 12d, 3mo, 2024.
func formatAge(t time.Time) string {
	age := time.Since(t)

	if age < time.Hour {
		return fmt.Sprintf("%dm", int(age.Minutes()))
	}

	if age < 24*time.Hour {
		return fmt.Sprintf("%dh", int(age.Hours()))
	}

	if age < 30*24*time.Hour {
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}

	if age < 365*24*time.Hour {
		return fmt.Sprintf("%dmo", int(age.Hours()/(24*30)))
	}

	return t.Format("2006")
}
