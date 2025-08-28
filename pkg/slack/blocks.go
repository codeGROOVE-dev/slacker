package slack

import (
	"fmt"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/slack-go/slack"
)

// BuildDashboardBlocks creates Slack blocks for the PR dashboard.
func BuildDashboardBlocks(userID string, prs []*state.PRState) []slack.Block {
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "Your Pull Requests", false, false),
		),
	}

	if len(prs) == 0 {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "_No pull requests found_", false, false),
			nil, nil,
		))
		return blocks
	}

	// Group PRs by status.
	var blockedOnYou, waitingOnOthers, other []*state.PRState
	for _, pr := range prs {
		switch pr.State {
		case "broken_heart", "carpentry_saw", "check":
			blockedOnYou = append(blockedOnYou, pr)
		case "hourglass":
			waitingOnOthers = append(waitingOnOthers, pr)
		default:
			other = append(other, pr)
		}
	}

	// Add blocked on you section.
	if len(blockedOnYou) > 0 {
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*🔥 Blocked on you:*", false, false),
			nil, nil,
		))
		for _, pr := range blockedOnYou {
			blocks = append(blocks, createPRBlock(pr))
		}
	}

	// Add waiting on others section.
	if len(waitingOnOthers) > 0 {
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*⏳ Waiting on others:*", false, false),
			nil, nil,
		))
		for _, pr := range waitingOnOthers {
			blocks = append(blocks, createPRBlock(pr))
		}
	}

	// Add other PRs.
	if len(other) > 0 {
		blocks = append(blocks, slack.NewDividerBlock())
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Other PRs:*", false, false),
			nil, nil,
		))
		for _, pr := range other {
			blocks = append(blocks, createPRBlock(pr))
		}
	}

	// Add footer with link to web dashboard.
	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewContextBlock(
		"",
		slack.NewTextBlockObject("mrkdwn",
			fmt.Sprintf("Last updated: %s | <https://dash.ready-to-review.dev/?user=%s|View web dashboard>",
				time.Now().Format("3:04 PM"), userID),
			false, false,
		),
	))

	return blocks
}

func createPRBlock(pr *state.PRState) slack.Block {
	// Map state to emoji
	var stateEmoji string
	switch pr.State {
	case "test_tube":
		stateEmoji = "🧪"
	case "broken_heart":
		stateEmoji = "💔"
	case "hourglass":
		stateEmoji = "⏳"
	case "carpentry_saw":
		stateEmoji = "🪚"
	case "check":
		stateEmoji = "✅"
	case "pray":
		stateEmoji = "🙏"
	case "face_palm":
		stateEmoji = "🤦"
	default:
		stateEmoji = "❓"
	}

	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.Owner, pr.Repo, pr.Number)

	text := fmt.Sprintf("%s <%s|%s/%s#%d>\n%s\nby @%s",
		stateEmoji,
		prURL,
		pr.Owner,
		pr.Repo,
		pr.Number,
		pr.Title,
		pr.Author,
	)

	if len(pr.BlockedOn) > 0 {
		text += fmt.Sprintf("\n_Blocked on: %v_", pr.BlockedOn)
	}

	return slack.NewSectionBlock(
		slack.NewTextBlockObject("mrkdwn", text, false, false),
		nil, nil,
	)
}

// BuildSettingsBlocks creates Slack blocks for user settings.
func BuildSettingsBlocks(prefs state.UserPreferences) []slack.Block {
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "Notification Settings", false, false),
		),
		slack.NewDividerBlock(),
	}

	// Real-time notifications toggle.
	realtimeText := "🔕 Disabled"
	if prefs.RealTimeNotifications {
		realtimeText = "🔔 Enabled"
	}
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Real-time notifications:* %s", realtimeText), false, false),
		nil,
		slack.NewAccessory(slack.NewButtonBlockElement(
			"toggle_realtime",
			"toggle_realtime",
			slack.NewTextBlockObject("plain_text", "Toggle", false, false),
		)),
	))

	// Channel notification delay.
	delayText := fmt.Sprintf("%d minutes", int(prefs.ChannelNotifyDelay.Minutes()))
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Notification delay after channel post:* %s", delayText), false, false),
		nil,
		slack.NewAccessory(slack.NewOverflowBlockElement(
			"change_delay",
			slack.NewOptionBlockObject("15", slack.NewTextBlockObject("plain_text", "15 minutes", false, false), nil),
			slack.NewOptionBlockObject("30", slack.NewTextBlockObject("plain_text", "30 minutes", false, false), nil),
			slack.NewOptionBlockObject("60", slack.NewTextBlockObject("plain_text", "1 hour", false, false), nil),
			slack.NewOptionBlockObject("120", slack.NewTextBlockObject("plain_text", "2 hours", false, false), nil),
		)),
	))

	// Daily reminders toggle.
	dailyText := "🔕 Disabled"
	if prefs.DailyReminders {
		dailyText = "🔔 Enabled (8-9am)"
	}
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*Daily reminders:* %s", dailyText), false, false),
		nil,
		slack.NewAccessory(slack.NewButtonBlockElement(
			"toggle_daily",
			"toggle_daily",
			slack.NewTextBlockObject("plain_text", "Toggle", false, false),
		)),
	))

	return blocks
}
