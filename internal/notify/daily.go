package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/github"
	"github.com/codeGROOVE-dev/slacker/internal/usermapping"
	"github.com/codeGROOVE-dev/slacker/pkg/home"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
)

// DigestUserMapper provides GitHub to Slack user mapping for daily digests.
// This interface enables testing of daily digest logic.
type DigestUserMapper interface {
	SlackHandle(ctx context.Context, githubUser, org, domain string) (string, error)
}

// TurnClient provides PR analysis functionality.
// This interface wraps turnclient for testing.
type TurnClient interface {
	Check(ctx context.Context, prURL, author string, updatedAt time.Time) (*turn.CheckResponse, error)
}

// defaultTurnClient implements TurnClient using the real turnclient.
type defaultTurnClient struct {
	client *turn.Client
}

func (d *defaultTurnClient) Check(ctx context.Context, prURL, author string, updatedAt time.Time) (*turn.CheckResponse, error) {
	return d.client.Check(ctx, prURL, author, updatedAt)
}

// DailyDigestScheduler handles sending daily digest DMs to users blocking PRs.
type DailyDigestScheduler struct {
	notifier         *Manager
	githubManager    github.ManagerInterface
	configManager    ConfigProvider
	stateStore       StateProvider
	slackManager     SlackManager
	turnClientFactory func(authToken string) (TurnClient, error) // Factory for creating TurnClient
}

// NewDailyDigestScheduler creates a new daily digest scheduler.
func NewDailyDigestScheduler(
	notifier *Manager,
	githubManager github.ManagerInterface,
	configManager ConfigProvider,
	stateStore StateProvider,
	slackManager SlackManager,
) *DailyDigestScheduler {
	return &DailyDigestScheduler{
		notifier:      notifier,
		githubManager: githubManager,
		configManager: configManager,
		stateStore:    stateStore,
		slackManager:  slackManager,
		turnClientFactory: func(authToken string) (TurnClient, error) {
			client, err := turn.NewDefaultClient()
			if err != nil {
				return nil, err
			}
			client.SetAuthToken(authToken)
			return &defaultTurnClient{client: client}, nil
		},
	}
}

// CheckAndSend checks all users and sends daily digests to those in the 8-9am window.
// Runs hourly to catch users across all timezones.
func (d *DailyDigestScheduler) CheckAndSend(ctx context.Context) {
	slog.Info("checking for daily digest candidates",
		"check_time", time.Now().Format(time.RFC3339))

	orgs := d.githubManager.AllOrgs()
	if len(orgs) == 0 {
		slog.Debug("no organizations configured, skipping daily digest check")
		return
	}

	totalSent := 0
	totalSkipped := 0
	totalErrors := 0

	for _, org := range orgs {
		// Check if daily reminders are enabled for this org
		if !d.configManager.DailyRemindersEnabled(org) {
			slog.Debug("daily reminders disabled for org", "org", org)
			totalSkipped++
			continue
		}

		sent, errors := d.processOrgDigests(ctx, org)
		totalSent += sent
		totalErrors += errors
	}

	slog.Info("daily digest check complete",
		"orgs_checked", len(orgs),
		"digests_sent", totalSent,
		"skipped", totalSkipped,
		"errors", totalErrors)
}

// processOrgDigests processes daily digests for all users in an organization.
func (d *DailyDigestScheduler) processOrgDigests(ctx context.Context, org string) (sent, errors int) {
	// Get GitHub client for this org
	githubClient, ok := d.githubManager.ClientForOrg(org)
	if !ok {
		slog.Warn("no GitHub client for org", "org", org)
		return 0, 1
	}

	// Get team ID from config (needed for Slack client)
	cfg, exists := d.configManager.Config(org)
	if !exists {
		slog.Warn("no config for org", "org", org)
		return 0, 1
	}
	teamID := cfg.Global.TeamID

	// Get Slack client for this workspace
	slackClient, err := d.slackManager.Client(ctx, teamID)
	if err != nil {
		slog.Warn("failed to get Slack client", "org", org, "team_id", teamID, "error", err)
		return 0, 1
	}

	// Create user mapper for this org
	userMapper := usermapping.New(slackClient.API(), githubClient.InstallationToken(ctx))

	// Create GraphQL client to fetch PRs (reuses existing shared implementation)
	token := githubClient.InstallationToken(ctx)
	gqlClient := github.NewGraphQLClient(ctx, token)

	// Get all open PRs for this org (using shared GraphQL query)
	snapshots, err := gqlClient.ListOpenPRs(ctx, org, 24)
	if err != nil {
		slog.Error("failed to fetch PRs for org", "org", org, "error", err)
		return 0, 1
	}

	// Convert PRSnapshot to home.PR format
	prs := make([]home.PR, 0, len(snapshots))
	for _, snap := range snapshots {
		prs = append(prs, home.PR{
			Number:     snap.Number,
			Title:      snap.Title,
			Author:     snap.Author,
			Repository: fmt.Sprintf("%s/%s", snap.Owner, snap.Repo),
			URL:        snap.URL,
			UpdatedAt:  snap.UpdatedAt,
		})
	}

	if len(prs) == 0 {
		slog.Debug("no open PRs for org", "org", org)
		return 0, 0
	}

	// Analyze each PR with turnclient to find blocked users
	blockedUsers := make(map[string][]home.PR) // githubUsername -> PRs they're blocking

	for i := range prs {
		pr := &prs[i]
		// Skip PRs older than 90 days (stale)
		if time.Since(pr.UpdatedAt) > 90*24*time.Hour {
			continue
		}

		// Skip if PR was updated very recently (already sent DMs)
		if time.Since(pr.UpdatedAt) < 8*time.Hour {
			continue
		}

		// Analyze with turnclient
		checkResult, err := d.analyzePR(ctx, githubClient, org, *pr)
		if err != nil {
			slog.Debug("failed to analyze PR", "org", org, "pr", pr.Number, "error", err)
			continue
		}

		// Find users who are blocking this PR
		for githubUser, action := range checkResult.Analysis.NextAction {
			if githubUser == "_system" {
				continue
			}

			// Enrich PR with turnclient data
			enrichedPR := d.enrichPR(*pr, checkResult, githubUser, action)
			blockedUsers[githubUser] = append(blockedUsers[githubUser], enrichedPR)
		}
	}

	slog.Debug("analyzed PRs for daily digest",
		"org", org,
		"total_prs", len(prs),
		"blocked_users", len(blockedUsers))

	// Send digests to users who are in their 8-9am window
	domain := d.configManager.Domain(org)
	for githubUser, userPRs := range blockedUsers {
		if d.shouldSendDigest(ctx, userMapper, slackClient, githubUser, org, domain, userPRs) {
			if err := d.sendDigest(ctx, userMapper, slackClient, githubUser, org, domain, userPRs); err != nil {
				slog.Error("failed to send daily digest",
					"org", org,
					"github_user", githubUser,
					"pr_count", len(userPRs),
					"error", err)
				errors++
			} else {
				sent++
			}
		}
	}

	return sent, errors
}

// analyzePR analyzes a PR with turnclient.
func (d *DailyDigestScheduler) analyzePR(ctx context.Context, githubClient github.ClientInterface, _ string, pr home.PR) (*turn.CheckResponse, error) {
	turnClient, err := d.turnClientFactory(githubClient.InstallationToken(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to create turn client: %w", err)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result, err := turnClient.Check(checkCtx, pr.URL, pr.Author, pr.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to check PR: %w", err)
	}

	return result, nil
}

// enrichPR enriches a PR with turnclient analysis results.
func (*DailyDigestScheduler) enrichPR(pr home.PR, _ *turn.CheckResponse, _ string, action turn.Action) home.PR {
	pr.ActionKind = string(action.Kind)
	pr.ActionReason = action.Reason
	pr.NeedsReview = action.Kind == "review" || action.Kind == "approve"
	pr.IsBlocked = true // They're in NextAction, so they're blocking

	return pr
}

// shouldSendDigest determines if a digest should be sent to a user now.
func (d *DailyDigestScheduler) shouldSendDigest(
	ctx context.Context, userMapper DigestUserMapper, slackClient SlackClient,
	githubUser, org, domain string, _ []home.PR,
) bool {
	// Map to Slack user
	slackUserID, err := userMapper.SlackHandle(ctx, githubUser, org, domain)
	if err != nil || slackUserID == "" {
		slog.Debug("no Slack mapping for GitHub user",
			"github_user", githubUser,
			"org", org)
		return false
	}

	// Get user's timezone
	tzName, err := slackClient.UserTimezone(ctx, slackUserID)
	if err != nil {
		slog.Debug("failed to get user timezone",
			"slack_user", slackUserID,
			"github_user", githubUser,
			"error", err)
		return false
	}

	// Parse timezone
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		slog.Debug("invalid timezone",
			"slack_user", slackUserID,
			"timezone", tzName,
			"error", err)
		return false
	}

	// Check if it's 8-9am in user's timezone
	now := time.Now().In(loc)
	hour := now.Hour()

	if hour < 8 || hour >= 9 {
		return false // Not in the 8-9am window
	}

	// Check if we already sent a digest today
	today := now.Format("2006-01-02")
	if lastDigest, exists := d.stateStore.LastDigest(slackUserID, today); exists {
		slog.Debug("already sent digest today",
			"slack_user", slackUserID,
			"github_user", githubUser,
			"today", today,
			"last_sent", lastDigest)
		return false
	}

	return true
}

// sendDigest sends a daily digest to a user.
func (d *DailyDigestScheduler) sendDigest(
	ctx context.Context, userMapper DigestUserMapper, slackClient SlackClient,
	githubUser, org, domain string, prs []home.PR,
) error {
	// Map to Slack user
	slackUserID, err := userMapper.SlackHandle(ctx, githubUser, org, domain)
	if err != nil {
		return fmt.Errorf("failed to map user: %w", err)
	}

	// Separate incoming (need to review) vs outgoing (user is author)
	var incoming, outgoing []home.PR
	for i := range prs {
		if prs[i].Author == githubUser {
			outgoing = append(outgoing, prs[i])
		} else {
			incoming = append(incoming, prs[i])
		}
	}

	// Sort both lists by most recently updated first
	sort.Slice(incoming, func(i, j int) bool {
		return incoming[i].UpdatedAt.After(incoming[j].UpdatedAt)
	})
	sort.Slice(outgoing, func(i, j int) bool {
		return outgoing[i].UpdatedAt.After(outgoing[j].UpdatedAt)
	})

	// Format digest message with separated sections
	message := d.formatDigestMessage(incoming, outgoing)

	// Send DM
	_, _, err = slackClient.SendDirectMessage(ctx, slackUserID, message)
	if err != nil {
		return fmt.Errorf("failed to send DM: %w", err)
	}

	// Record that we sent a digest today (memory + best-effort persistence)
	tzName, tzErr := slackClient.UserTimezone(ctx, slackUserID)
	if tzErr != nil {
		slog.Debug("failed to get user timezone, using UTC", "user", slackUserID, "error", tzErr)
		tzName = "UTC"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		slog.Debug("failed to load timezone location, using UTC", "timezone", tzName, "error", err)
		loc = time.UTC
	}
	today := time.Now().In(loc).Format("2006-01-02")

	// RecordDigest always succeeds (memory) and attempts persistence (best-effort)
	if err := d.stateStore.RecordDigest(slackUserID, today, time.Now()); err != nil {
		slog.Debug("state store returned error for RecordDigest", "error", err)
	}

	slog.Info("sent daily digest",
		"slack_user", slackUserID,
		"github_user", githubUser,
		"incoming_count", len(incoming),
		"outgoing_count", len(outgoing))

	return nil
}

// formatDigestMessage formats a daily digest message with friendly, varied greetings.
func (d *DailyDigestScheduler) formatDigestMessage(incoming, outgoing []home.PR) string {
	return d.formatDigestMessageAt(incoming, outgoing, time.Now())
}

// formatDigestMessageAt formats a daily digest message at a specific time (for testing).
func (*DailyDigestScheduler) formatDigestMessageAt(incoming, outgoing []home.PR, now time.Time) string {
	var sb strings.Builder

	// Friendly, happy greetings - keep it chill and inviting
	greetings := []string{
		"☀️ *Good morning!*",
		"👋 *Hey there!*",
		"☕ *Coffee's ready!*",
		"🌈 *Happy morning!*",
		"🎨 *Time to create!*",
		"🌻 *Hello sunshine!*",
		"🎵 *Morning vibes!*",
		"✨ *Hey friend!*",
		"🌸 *Beautiful day!*",
		"💫 *Greetings!*",
	}

	// Pick greeting based on time for variety
	greetingIdx := (now.Hour()*60 + now.Minute()) % len(greetings)
	sb.WriteString(greetings[greetingIdx])
	sb.WriteString("\n\n")

	// Show incoming PRs (user needs to review)
	if len(incoming) > 0 {
		sb.WriteString("*To Review:*\n")
		for i := range incoming {
			sb.WriteString(fmt.Sprintf(":hourglass: <%s|%s> · %s\n",
				incoming[i].URL,
				incoming[i].Title,
				incoming[i].ActionKind))
		}
		sb.WriteString("\n")
	}

	// Show outgoing PRs (user is author, needs to address feedback)
	if len(outgoing) > 0 {
		sb.WriteString("*Your PRs:*\n")
		for i := range outgoing {
			sb.WriteString(fmt.Sprintf(":hourglass: <%s|%s> · %s\n",
				outgoing[i].URL,
				outgoing[i].Title,
				outgoing[i].ActionKind))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("_Your daily digest from Ready to Review_")

	return sb.String()
}
