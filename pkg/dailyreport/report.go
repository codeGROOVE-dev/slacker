// Package dailyreport provides functionality for generating and sending daily PR reports to users.
package dailyreport

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/home"
	"github.com/slack-go/slack"
)

const (
	// MinHoursBetweenReports is the minimum time between daily reports (23 hours).
	MinHoursBetweenReports = 23

	// WindowStartHour is when the daily report window opens (6am local time).
	WindowStartHour = 6

	// WindowEndHour is when the daily report window closes (11:30am local time).
	// We use hour 11 and check minutes separately to support 11:30am cutoff.
	WindowEndHour = 12 // Exclusive, so 11:59am is the last valid time
)

// UserBlockingInfo contains information about PRs a user is blocking.
type UserBlockingInfo struct {
	GitHubUsername string
	SlackUserID    string
	IncomingPRs    []home.PR // PRs user needs to review
	OutgoingPRs    []home.PR // PRs user authored that need action
}

// StateStore handles persistence of report send times.
type StateStore interface {
	LastReportSent(ctx context.Context, userID string) (time.Time, bool)
	RecordReportSent(ctx context.Context, userID string, sentAt time.Time) error
}

// SlackClient handles Slack API operations.
type SlackClient interface {
	SendDirectMessageWithBlocks(ctx context.Context, userID string, blocks []slack.Block) (dmChannelID, messageTS string, err error)
	UserTimezone(ctx context.Context, userID string) (string, error)
	IsUserActive(ctx context.Context, userID string) bool
}

// Sender handles sending daily reports to users.
type Sender struct {
	stateStore  StateStore
	slackClient SlackClient
}

// NewSender creates a new daily report sender.
func NewSender(stateStore StateStore, slackClient SlackClient) *Sender {
	return &Sender{
		stateStore:  stateStore,
		slackClient: slackClient,
	}
}

// ShouldSendReport determines if a report should be sent to a user now.
func (s *Sender) ShouldSendReport(ctx context.Context, userInfo UserBlockingInfo) bool {
	// Must have PRs to report
	if len(userInfo.IncomingPRs) == 0 && len(userInfo.OutgoingPRs) == 0 {
		return false
	}

	// Check when we last sent a report
	lastSent, exists := s.stateStore.LastReportSent(ctx, userInfo.SlackUserID)
	if exists {
		hoursSince := time.Since(lastSent).Hours()
		if hoursSince < MinHoursBetweenReports {
			slog.Debug("skipping report - sent too recently",
				"user", userInfo.SlackUserID,
				"hours_since_last", hoursSince,
				"min_hours", MinHoursBetweenReports)
			return false
		}
	}

	// Get user's timezone
	tzName, err := s.slackClient.UserTimezone(ctx, userInfo.SlackUserID)
	if err != nil {
		slog.Debug("failed to get user timezone",
			"user", userInfo.SlackUserID,
			"error", err)
		return false
	}

	// Parse timezone
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		slog.Debug("invalid timezone",
			"user", userInfo.SlackUserID,
			"timezone", tzName,
			"error", err)
		return false
	}

	// Check if it's within the 6am-11:30am window in user's timezone
	now := time.Now().In(loc)
	h := now.Hour()
	m := now.Minute()

	// Window is 6:00am - 11:29am (before 11:30am)
	outsideWindow := h < WindowStartHour ||
		h >= WindowEndHour ||
		(h == 11 && m >= 30)

	if outsideWindow {
		slog.Debug("skipping report - outside time window",
			"user", userInfo.SlackUserID,
			"time", fmt.Sprintf("%02d:%02d", h, m),
			"window", "6:00am-11:29am")
		return false
	}

	// Check if user is currently active
	if !s.slackClient.IsUserActive(ctx, userInfo.SlackUserID) {
		slog.Debug("skipping report - user not active",
			"user", userInfo.SlackUserID)
		return false
	}

	return true
}

// SendReport sends a daily report to a user.
func (s *Sender) SendReport(ctx context.Context, userInfo UserBlockingInfo) error {
	// Build Block Kit blocks for the report
	blocks := BuildReportBlocks(userInfo.IncomingPRs, userInfo.OutgoingPRs)

	// Send the DM with blocks
	_, _, err := s.slackClient.SendDirectMessageWithBlocks(ctx, userInfo.SlackUserID, blocks)
	if err != nil {
		return fmt.Errorf("failed to send DM: %w", err)
	}

	// Record that we sent the report
	if err := s.stateStore.RecordReportSent(ctx, userInfo.SlackUserID, time.Now()); err != nil {
		slog.Warn("failed to record report send time",
			"user", userInfo.SlackUserID,
			"error", err)
		// Don't fail - report was sent successfully
	}

	slog.Info("sent daily report",
		"user", userInfo.SlackUserID,
		"github_user", userInfo.GitHubUsername,
		"incoming_count", len(userInfo.IncomingPRs),
		"outgoing_count", len(userInfo.OutgoingPRs))

	return nil
}

// randomGreeting returns a friendly greeting based on the current time of day.
// The greeting is deterministic for a given time, so it won't change if called multiple times
// in the same minute.
func randomGreeting() string {
	now := time.Now()
	h := now.Hour()

	var greetings []string

	switch {
	case h >= 6 && h < 12:
		// Morning greetings (6am-12pm)
		greetings = []string{
			"☀️ *Good morning!*",
			"☕ *Coffee's ready!*",
			"🌈 *Happy morning!*",
			"🌻 *Hello sunshine!*",
			"🎵 *Morning vibes!*",
			"🌸 *Beautiful day!*",
			"🇫🇷 *Bonjour!*",
			"🇯🇵 *Ohayō!*",
			"🇪🇸 *Buenos días!*",
			"🇮🇹 *Buongiorno!*",
		}
	case h >= 12 && h < 17:
		// Afternoon greetings (12pm-5pm)
		greetings = []string{
			"👋 *Hey there!*",
			"☀️ *Good afternoon!*",
			"🎨 *Time to create!*",
			"✨ *Hey friend!*",
			"💫 *Greetings!*",
			"🌟 *Looking good!*",
			"🇧🇷 *Oi! Tudo bem?*",
			"🇩🇪 *Guten Tag!*",
			"🇮🇳 *Namaste!*",
			"🌺 *Aloha!*",
		}
	case h >= 17 && h < 22:
		// Evening greetings (5pm-10pm)
		greetings = []string{
			"🌆 *Good evening!*",
			"👋 *Hey there!*",
			"✨ *Hey friend!*",
			"💫 *Greetings!*",
			"🌙 *Evening check-in!*",
			"⭐ *Still going strong!*",
			"🇮🇹 *Buonasera!*",
			"🇫🇷 *Bonsoir!*",
			"🇪🇸 *Buenas tardes!*",
			"🇰🇪 *Habari!*",
		}
	default:
		// Late night/early morning (10pm-6am)
		greetings = []string{
			"🌙 *Burning the midnight oil?*",
			"⭐ *Night owl!*",
			"✨ *Hey there!*",
			"💫 *Greetings!*",
			"🦉 *Still at it!*",
			"🌟 *Late night vibes!*",
			"🇯🇵 *Konbanwa!*",
			"🇸🇪 *Hej hej!*",
			"🇹🇭 *Sawasdee!*",
			"🇮🇱 *Shalom!*",
		}
	}

	// Pick greeting based on time for variety
	greetingIdx := (now.Hour()*60 + now.Minute()) % len(greetings)
	return greetings[greetingIdx]
}

// formatPRLine formats a single PR as a line of text (goose-inspired format).
// Returns a string like: "■ repo#123 • title — action" or "  repo#456 • title".
func formatPRLine(pr *home.PR) string {
	// Extract repo name from "org/repo" format
	parts := strings.SplitN(pr.Repository, "/", 2)
	repo := pr.Repository
	if len(parts) == 2 {
		repo = parts[1]
	}

	// Determine bullet character based on blocking status
	var bullet string
	switch {
	case pr.IsBlocked || pr.NeedsReview:
		// Critical: blocked on user
		bullet = "■"
	case pr.ActionKind != "":
		// Non-critical: has action but not blocking
		bullet = "•"
	default:
		// No action for user - use 2-space indent to align with bullets
		bullet = " "
	}

	// Build PR reference with link
	ref := fmt.Sprintf("<%s|%s#%d>", pr.URL, repo, pr.Number)

	// Build line: bullet repo#123 • title
	line := fmt.Sprintf("%s %s • %s", bullet, ref, pr.Title)

	// Add action kind if present (only show user's next action)
	if pr.ActionKind != "" {
		action := strings.ReplaceAll(pr.ActionKind, "_", " ")
		line = fmt.Sprintf("%s — %s", line, action)
	}

	return line
}

// BuildReportBlocks creates Block Kit blocks for a daily report.
// Format inspired by goose - simple, minimal, action-focused.
func BuildReportBlocks(incoming, outgoing []home.PR) []slack.Block {
	// Sort PRs by most recently updated first (make copies to avoid modifying input)
	incomingSorted := make([]home.PR, len(incoming))
	copy(incomingSorted, incoming)
	sort.Slice(incomingSorted, func(i, j int) bool {
		return incomingSorted[i].UpdatedAt.After(incomingSorted[j].UpdatedAt)
	})

	outgoingSorted := make([]home.PR, len(outgoing))
	copy(outgoingSorted, outgoing)
	sort.Slice(outgoingSorted, func(i, j int) bool {
		return outgoingSorted[i].UpdatedAt.After(outgoingSorted[j].UpdatedAt)
	})

	var blocks []slack.Block

	// Greeting
	greeting := randomGreeting()
	blocks = append(blocks,
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("%s Here is your daily report:", greeting), false, false),
			nil,
			nil,
		),
	)

	// Incoming PRs section (only if there are incoming PRs)
	if len(incomingSorted) > 0 {
		// Count blocked PRs
		n := 0
		for i := range incomingSorted {
			if incomingSorted[i].IsBlocked || incomingSorted[i].NeedsReview {
				n++
			}
		}

		// Section header
		header := "*Incoming*"
		if n > 0 {
			if n == 1 {
				header = "*Incoming — 1 blocked on you*"
			} else {
				header = fmt.Sprintf("*Incoming — %d blocked on you*", n)
			}
		}

		// Build PR list
		var prLines []string
		for i := range incomingSorted {
			prLines = append(prLines, formatPRLine(&incomingSorted[i]))
		}

		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", header+"\n\n"+strings.Join(prLines, "\n"), false, false),
				nil,
				nil,
			),
		)
	}

	// Outgoing PRs section (only if there are outgoing PRs)
	if len(outgoingSorted) > 0 {
		// Count blocked PRs
		n := 0
		for i := range outgoingSorted {
			if outgoingSorted[i].IsBlocked {
				n++
			}
		}

		// Section header
		header := "*Outgoing*"
		if n > 0 {
			if n == 1 {
				header = "*Outgoing — 1 blocked on you*"
			} else {
				header = fmt.Sprintf("*Outgoing — %d blocked on you*", n)
			}
		}

		// Build PR list
		var prLines []string
		for i := range outgoingSorted {
			prLines = append(prLines, formatPRLine(&outgoingSorted[i]))
		}

		blocks = append(blocks,
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", header+"\n\n"+strings.Join(prLines, "\n"), false, false),
				nil,
				nil,
			),
		)
	}

	return blocks
}
