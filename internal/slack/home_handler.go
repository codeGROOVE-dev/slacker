package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/codeGROOVE-dev/slacker/internal/config"
	"github.com/codeGROOVE-dev/slacker/internal/github"
	"github.com/codeGROOVE-dev/slacker/internal/state"
	"github.com/codeGROOVE-dev/slacker/pkg/home"
)

// HomeHandler handles app_home_opened events for a workspace.
type HomeHandler struct {
	slackManager  *Manager
	githubManager *github.Manager
	configManager *config.Manager
	stateStore    state.Store
}

// NewHomeHandler creates a new home view handler.
func NewHomeHandler(
	slackManager *Manager,
	githubManager *github.Manager,
	configManager *config.Manager,
	stateStore state.Store,
) *HomeHandler {
	return &HomeHandler{
		slackManager:  slackManager,
		githubManager: githubManager,
		configManager: configManager,
		stateStore:    stateStore,
	}
}

// HandleAppHomeOpened updates the app home view when a user opens it.
func (h *HomeHandler) HandleAppHomeOpened(ctx context.Context, teamID, slackUserID string) error {
	slog.Debug("handling app home opened",
		"team_id", teamID,
		"slack_user_id", slackUserID)

	// Get Slack client for this workspace
	slackClient, err := h.slackManager.Client(ctx, teamID)
	if err != nil {
		return fmt.Errorf("failed to get Slack client: %w", err)
	}

	// Get Slack user info to extract email
	slackUser, err := slackClient.API().GetUserInfo(slackUserID)
	if err != nil {
		slog.Warn("failed to get Slack user info", "user_id", slackUserID, "error", err)
		return h.publishPlaceholderHome(slackClient, slackUserID)
	}

	// Extract GitHub username from email (simple heuristic: part before @)
	// Works for "username@company.com" -> "username"
	email := slackUser.Profile.Email
	atIndex := strings.IndexByte(email, '@')
	if atIndex <= 0 {
		slog.Warn("could not extract GitHub username from Slack email",
			"slack_user_id", slackUserID,
			"email", email)
		return h.publishPlaceholderHome(slackClient, slackUserID)
	}
	githubUsername := email[:atIndex]

	// Get all orgs for this workspace
	workspaceOrgs := h.workspaceOrgs(teamID)
	if len(workspaceOrgs) == 0 {
		slog.Warn("no workspace orgs found", "team_id", teamID)
		return h.publishPlaceholderHome(slackClient, slackUserID)
	}

	// Get GitHub client for first org (they all share the same app)
	githubClient, ok := h.githubManager.ClientForOrg(workspaceOrgs[0])
	if !ok {
		return fmt.Errorf("no GitHub client for org: %s", workspaceOrgs[0])
	}

	// Create fetcher and fetch dashboard
	fetcher := home.NewFetcher(
		githubClient.Client(),
		h.stateStore,
		githubClient.InstallationToken(ctx),
		"ready-to-review[bot]",
	)

	dashboard, err := fetcher.FetchDashboard(ctx, githubUsername, workspaceOrgs)
	if err != nil {
		slog.Error("failed to fetch dashboard",
			"github_user", githubUsername,
			"error", err)
		return h.publishPlaceholderHome(slackClient, slackUserID)
	}

	// Build Block Kit UI - use first org as primary
	blocks := home.BuildBlocks(dashboard, workspaceOrgs[0])

	// Publish to Slack
	if err := slackClient.PublishHomeView(slackUserID, blocks); err != nil {
		return fmt.Errorf("failed to publish home view: %w", err)
	}

	slog.Info("published home view",
		"slack_user_id", slackUserID,
		"github_user", githubUsername,
		"incoming_prs", len(dashboard.IncomingPRs),
		"outgoing_prs", len(dashboard.OutgoingPRs),
		"workspace_orgs", len(workspaceOrgs))

	return nil
}

// workspaceOrgs returns all GitHub orgs configured for this Slack workspace.
func (h *HomeHandler) workspaceOrgs(teamID string) []string {
	allOrgs := h.githubManager.AllOrgs()
	var workspaceOrgs []string

	for _, org := range allOrgs {
		cfg, exists := h.configManager.Config(org)
		if !exists {
			continue
		}

		// Check if this org is configured for this workspace
		if cfg.Global.TeamID == teamID {
			workspaceOrgs = append(workspaceOrgs, org)
		}
	}

	return workspaceOrgs
}

// publishPlaceholderHome publishes a simple placeholder home view.
func (*HomeHandler) publishPlaceholderHome(slackClient *Client, slackUserID string) error {
	slog.Debug("publishing placeholder home", "user_id", slackUserID)

	blocks := home.BuildBlocks(&home.Dashboard{
		IncomingPRs:   nil,
		OutgoingPRs:   nil,
		WorkspaceOrgs: []string{"your-org"},
	}, "your-org")

	return slackClient.PublishHomeView(slackUserID, blocks)
}
