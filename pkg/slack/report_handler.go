package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/dailyreport"
	"github.com/codeGROOVE-dev/slacker/pkg/home"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	gogithub "github.com/google/go-github/v50/github"
)

// ReportHandler handles manual daily report generation via slash command.
type ReportHandler struct {
	slackManager   *Manager
	githubManager  GitHubManager
	stateStore     state.Store
	reverseMapping UserMapper
}

// NewReportHandler creates a new report handler.
func NewReportHandler(
	slackManager *Manager,
	githubManager GitHubManager,
	stateStore state.Store,
	reverseMapping UserMapper,
) *ReportHandler {
	return &ReportHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		stateStore:     stateStore,
		reverseMapping: reverseMapping,
	}
}

// HandleReportCommand handles the /goose report slash command.
// It generates and sends a daily report for the requesting user, bypassing time window and interval checks.
func (h *ReportHandler) HandleReportCommand(ctx context.Context, teamID, slackUserID string) error {
	slog.Info("handling manual daily report request",
		"team_id", teamID,
		"slack_user", slackUserID)

	// Get Slack client for the workspace
	slackClient, err := h.slackManager.Client(ctx, teamID)
	if err != nil {
		slog.Error("failed to get Slack client for report",
			"team_id", teamID,
			"error", err)
		return fmt.Errorf("failed to get Slack client: %w", err)
	}

	// Try to map Slack user to GitHub username
	// We need to check all orgs since we don't know which org the user belongs to
	var githubUsername string
	var foundOrg string

	// Get workspace info to determine workspace name
	workspaceInfo, err := slackClient.WorkspaceInfo(ctx)
	if err != nil {
		slog.Error("failed to get workspace info for user mapping",
			"team_id", teamID,
			"error", err)
		return fmt.Errorf("failed to get workspace info: %w", err)
	}
	workspaceName := workspaceInfo.Domain

	// Get underlying Slack API client for user mapping
	slackAPI := slackClient.API()
	if slackAPI == nil {
		slog.Error("failed to get Slack API client for user mapping",
			"team_id", teamID)
		return errors.New("failed to get Slack API client")
	}

	allOrgs := h.githubManager.AllOrgs()
	slog.Info("attempting to map Slack user to GitHub",
		"slack_user", slackUserID,
		"workspace", workspaceName,
		"available_orgs", allOrgs,
		"org_count", len(allOrgs))

	// Collect all orgs where this user is a member
	var userOrgs []string
	for _, org := range allOrgs {
		mapping, err := h.reverseMapping.LookupGitHub(ctx, slackAPI, slackUserID, org, workspaceName)
		if err != nil || mapping == nil || mapping.GitHubUsername == "" {
			slog.Info("org did not match user mapping",
				"slack_user", slackUserID,
				"org", org,
				"error", err)
			continue
		}

		// First match establishes the GitHub username
		if githubUsername == "" {
			githubUsername = mapping.GitHubUsername
			foundOrg = org
			slog.Info("mapped Slack user to GitHub for report",
				"slack_user", slackUserID,
				"github_user", githubUsername,
				"org", org,
				"match_method", mapping.MatchMethod,
				"confidence", mapping.Confidence)
			userOrgs = append(userOrgs, org)
			continue
		}

		// Check if subsequent matches have the same GitHub username
		if mapping.GitHubUsername != githubUsername {
			// Different GitHub username in this org - skip it
			slog.Warn("user maps to different GitHub username in org, skipping",
				"slack_user", slackUserID,
				"org", org,
				"expected_github_user", githubUsername,
				"found_github_user", mapping.GitHubUsername)
			continue
		}

		// Same username in another org - include it
		slog.Info("found user in additional org",
			"slack_user", slackUserID,
			"github_user", githubUsername,
			"org", org,
			"match_method", mapping.MatchMethod,
			"confidence", mapping.Confidence)
		userOrgs = append(userOrgs, org)
	}

	if githubUsername == "" {
		slog.Warn("cannot generate report: Slack user not mapped to any GitHub user",
			"slack_user", slackUserID,
			"workspace", workspaceName,
			"checked_orgs", allOrgs)
		return fmt.Errorf("could not find GitHub username for Slack user %s", slackUserID)
	}

	slog.Info("collected user orgs for report",
		"slack_user", slackUserID,
		"github_user", githubUsername,
		"orgs", userOrgs,
		"org_count", len(userOrgs))

	// Get GitHub client for the org
	ghClient, ok := h.githubManager.ClientForOrg(foundOrg)
	if !ok {
		slog.Error("no GitHub client for org", "org", foundOrg)
		return fmt.Errorf("no GitHub client for org %s", foundOrg)
	}

	// Get GitHub token
	token := ghClient.InstallationToken(ctx)

	// Create dashboard fetcher - need to cast to *github.Client from go-github package
	goGitHubClient, ok := ghClient.Client().(*gogithub.Client)
	if !ok {
		slog.Error("failed to cast GitHub client", "org", foundOrg)
		return fmt.Errorf("failed to cast GitHub client for org %s", foundOrg)
	}
	fetcher := home.NewFetcher(goGitHubClient, h.stateStore, token, "reviewgoose[bot]")

	// Fetch dashboard for user across all orgs where they're a member
	slog.Info("fetching dashboard for manual report",
		"slack_user", slackUserID,
		"github_user", githubUsername,
		"orgs", userOrgs)
	dashboard, err := fetcher.FetchDashboard(ctx, githubUsername, userOrgs)
	if err != nil {
		slog.Error("failed to fetch dashboard for manual report",
			"slack_user", slackUserID,
			"github_user", githubUsername,
			"error", err)
		return fmt.Errorf("failed to fetch dashboard: %w", err)
	}

	slog.Info("dashboard fetched successfully",
		"slack_user", slackUserID,
		"github_user", githubUsername,
		"incoming_prs", len(dashboard.IncomingPRs),
		"outgoing_prs", len(dashboard.OutgoingPRs),
		"workspace_orgs", dashboard.WorkspaceOrgs)

	// If user has no PRs, send a friendly message
	if len(dashboard.IncomingPRs) == 0 && len(dashboard.OutgoingPRs) == 0 {
		slog.Info("user has no PRs for report",
			"slack_user", slackUserID,
			"github_user", githubUsername)
		_, _, err := slackClient.SendDirectMessage(ctx, slackUserID,
			"🎉 You're all caught up! No pending PR reviews or outgoing PRs right now.")
		if err != nil {
			slog.Error("failed to send empty report message",
				"slack_user", slackUserID,
				"error", err)
			return fmt.Errorf("failed to send message: %w", err)
		}
		// Record that we sent a report (even if empty)
		if err := h.stateStore.RecordReportSent(ctx, slackUserID, time.Now()); err != nil {
			slog.Warn("failed to record empty report timestamp",
				"slack_user", slackUserID,
				"error", err)
		}
		return nil
	}

	// Build report blocks
	blocks := dailyreport.BuildReportBlocks(dashboard.IncomingPRs, dashboard.OutgoingPRs)

	// Send the report
	_, _, err = slackClient.SendDirectMessageWithBlocks(ctx, slackUserID, blocks)
	if err != nil {
		slog.Error("failed to send manual daily report",
			"slack_user", slackUserID,
			"github_user", githubUsername,
			"incoming_prs", len(dashboard.IncomingPRs),
			"outgoing_prs", len(dashboard.OutgoingPRs),
			"error", err)
		return fmt.Errorf("failed to send report: %w", err)
	}

	slog.Info("successfully sent manual daily report",
		"slack_user", slackUserID,
		"github_user", githubUsername,
		"incoming_prs", len(dashboard.IncomingPRs),
		"outgoing_prs", len(dashboard.OutgoingPRs))

	// Record that we sent a report
	if err := h.stateStore.RecordReportSent(ctx, slackUserID, time.Now()); err != nil {
		slog.Warn("failed to record report timestamp",
			"slack_user", slackUserID,
			"error", err)
		// Don't fail the whole operation if we can't record the timestamp
	}

	return nil
}
