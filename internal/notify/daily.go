package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/codeGROOVE-dev/slacker/internal/config"
	"github.com/codeGROOVE-dev/slacker/internal/github"
	slackpkg "github.com/codeGROOVE-dev/slacker/internal/slack"
	"github.com/codeGROOVE-dev/slacker/internal/usermapping"
	"github.com/codeGROOVE-dev/slacker/pkg/home"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	gh "github.com/google/go-github/v50/github"
)

// ConfigProvider provides configuration for daily digests.
type ConfigProvider interface {
	DailyRemindersEnabled(org string) bool
	Domain(org string) string
	Config(org string) (*config.RepoConfig, bool)
}

// StateProvider provides state storage for daily digests.
type StateProvider interface {
	LastDigest(userID, date string) (time.Time, bool)
	RecordDigest(userID, date string, sentAt time.Time) error
	LastDM(userID, prURL string) (time.Time, bool)
}

// SlackManager provides Slack client operations across workspaces.
type SlackManager interface {
	Client(ctx context.Context, teamID string) (*slackpkg.Client, error)
}

// DailyDigestScheduler handles sending daily digest DMs to users blocking PRs.
type DailyDigestScheduler struct {
	notifier      *Manager
	githubManager *github.Manager
	configManager ConfigProvider
	stateStore    StateProvider
	slackManager  SlackManager
}

// NewDailyDigestScheduler creates a new daily digest scheduler.
func NewDailyDigestScheduler(
	notifier *Manager,
	githubManager *github.Manager,
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

	// Get all open PRs for this org
	prs, err := d.fetchOrgPRs(ctx, githubClient, org)
	if err != nil {
		slog.Error("failed to fetch PRs for org", "org", org, "error", err)
		return 0, 1
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

// fetchOrgPRs fetches all open PRs for an organization.
func (d *DailyDigestScheduler) fetchOrgPRs(ctx context.Context, githubClient *github.Client, org string) ([]home.PR, error) {
	client := githubClient.Client()

	// Search for all open PRs in this org
	query := fmt.Sprintf("is:pr is:open org:%s", org)
	opts := &gh.SearchOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}

	var allPRs []home.PR

	for {
		var result *gh.IssuesSearchResult
		var resp *gh.Response

		// Retry GitHub API call with exponential backoff
		err := retry.Do(
			func() error {
				var searchErr error
				result, resp, searchErr = client.Search.Issues(ctx, query, opts)
				return searchErr
			},
			retry.Attempts(5),
			retry.Delay(time.Second),
			retry.MaxDelay(2*time.Minute),
			retry.DelayType(retry.BackOffDelay),
			retry.OnRetry(func(n uint, err error) {
				slog.Warn("retrying GitHub search after failure",
					"org", org,
					"attempt", n+1,
					"error", err)
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to search PRs after retries: %w", err)
		}

		for _, issue := range result.Issues {
			if issue.PullRequestLinks == nil {
				continue // Skip non-PRs
			}

			// Extract repo from URL
			parts := strings.Split(*issue.RepositoryURL, "/")
			if len(parts) < 2 {
				continue
			}
			repo := parts[len(parts)-1]

			allPRs = append(allPRs, home.PR{
				Number:     *issue.Number,
				Title:      *issue.Title,
				Author:     *issue.User.Login,
				Repository: fmt.Sprintf("%s/%s", org, repo),
				URL:        *issue.HTMLURL,
				UpdatedAt:  issue.UpdatedAt.Time,
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allPRs, nil
}

// analyzePR analyzes a PR with turnclient.
func (d *DailyDigestScheduler) analyzePR(ctx context.Context, githubClient *github.Client, org string, pr home.PR) (*turn.CheckResponse, error) {
	turnClient, err := turn.NewDefaultClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create turn client: %w", err)
	}

	turnClient.SetAuthToken(githubClient.InstallationToken(ctx))

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Retry turnclient call with exponential backoff
	var result *turn.CheckResponse
	err = retry.Do(
		func() error {
			var checkErr error
			result, checkErr = turnClient.Check(checkCtx, pr.URL, pr.Author, pr.UpdatedAt)
			return checkErr
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			slog.Warn("retrying turnclient check after failure",
				"pr", pr.URL,
				"attempt", n+1,
				"error", err)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to check PR after retries: %w", err)
	}

	return result, nil
}

// enrichPR enriches a PR with turnclient analysis results.
func (d *DailyDigestScheduler) enrichPR(pr home.PR, checkResult *turn.CheckResponse, githubUser string, action turn.Action) home.PR {
	pr.ActionKind = string(action.Kind)
	pr.ActionReason = action.Reason
	pr.NeedsReview = action.Kind == "review" || action.Kind == "approve"
	pr.IsBlocked = true // They're in NextAction, so they're blocking

	return pr
}

// shouldSendDigest determines if a digest should be sent to a user now.
func (d *DailyDigestScheduler) shouldSendDigest(ctx context.Context, userMapper *usermapping.Service, slackClient *slackpkg.Client, githubUser, org, domain string, prs []home.PR) bool {
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
func (d *DailyDigestScheduler) sendDigest(ctx context.Context, userMapper *usermapping.Service, slackClient *slackpkg.Client, githubUser, org, domain string, prs []home.PR) error {
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
func (d *DailyDigestScheduler) formatDigestMessageAt(incoming, outgoing []home.PR, now time.Time) string {
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
