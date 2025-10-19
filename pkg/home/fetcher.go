package home

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/codeGROOVE-dev/slacker/internal/state"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/google/go-github/v50/github"
)

const (
	stalePRThreshold = 90 * 24 * time.Hour // 90 days
)

// Fetcher retrieves and enriches PRs for the home dashboard.
type Fetcher struct {
	githubClient *github.Client
	stateStore   state.Store
	githubToken  string
	botUsername  string
}

// NewFetcher creates a new PR fetcher.
func NewFetcher(githubClient *github.Client, stateStore state.Store, githubToken, botUsername string) *Fetcher {
	return &Fetcher{
		githubClient: githubClient,
		stateStore:   stateStore,
		githubToken:  githubToken,
		botUsername:  botUsername,
	}
}

// FetchDashboard fetches all PRs for a GitHub user across workspace organizations.
// Degrades gracefully - returns partial data on errors rather than failing completely.
func (f *Fetcher) FetchDashboard(ctx context.Context, githubUsername string, workspaceOrgs []string) (*Dashboard, error) {
	slog.Debug("fetching dashboard for user",
		"github_user", githubUsername,
		"workspace_orgs", workspaceOrgs)

	dashboard := &Dashboard{
		WorkspaceOrgs: workspaceOrgs,
	}

	// Fetch all open PRs involving this user across workspace orgs
	// Continues even if some orgs fail - returns what we can get
	incoming, outgoing := f.fetchUserPRs(ctx, githubUsername, workspaceOrgs)

	slog.Debug("fetched PRs from GitHub",
		"github_user", githubUsername,
		"incoming_count", len(incoming),
		"outgoing_count", len(outgoing))

	// Enrich with turnclient data
	// Degrades gracefully - returns basic PR data if turnclient fails
	dashboard.IncomingPRs = f.enrichPRs(ctx, incoming, githubUsername, true)
	dashboard.OutgoingPRs = f.enrichPRs(ctx, outgoing, githubUsername, false)

	slog.Debug("enriched PRs with turnclient",
		"github_user", githubUsername,
		"incoming_enriched", len(dashboard.IncomingPRs),
		"outgoing_enriched", len(dashboard.OutgoingPRs))

	// Sort: blocked first, then by recency
	sortPRs(dashboard.IncomingPRs)
	sortPRs(dashboard.OutgoingPRs)

	return dashboard, nil
}

// fetchUserPRs fetches incoming and outgoing PRs for a user.
func (f *Fetcher) fetchUserPRs(ctx context.Context, githubUsername string, workspaceOrgs []string) (incoming, outgoing []PR) {
	// Validate inputs to prevent injection in GitHub search queries
	if githubUsername == "" || strings.ContainsAny(githubUsername, " \t\n\"'") {
		slog.Warn("invalid GitHub username", "username", githubUsername)
		return nil, nil
	}

	staleThreshold := time.Now().Add(-stalePRThreshold)

	// Search for PRs across all workspace orgs
	for _, org := range workspaceOrgs {
		// Validate org name
		if org == "" || strings.ContainsAny(org, " \t\n\"'") {
			slog.Warn("invalid org name, skipping", "org", org)
			continue
		}

		// Fetch authored PRs (outgoing)
		authorQuery := fmt.Sprintf("is:pr is:open author:%s org:%s", githubUsername, org)
		authoredPRs, err := f.searchPRs(ctx, authorQuery)
		if err != nil {
			slog.Warn("failed to search authored PRs", "org", org, "error", err)
			continue
		}

		for i := range authoredPRs {
			if authoredPRs[i].UpdatedAt.Before(staleThreshold) {
				continue // Skip stale PRs
			}
			outgoing = append(outgoing, authoredPRs[i])
		}

		// Fetch review-requested PRs (incoming)
		reviewQuery := fmt.Sprintf("is:pr is:open review-requested:%s org:%s", githubUsername, org)
		reviewPRs, err := f.searchPRs(ctx, reviewQuery)
		if err != nil {
			slog.Warn("failed to search review-requested PRs", "org", org, "error", err)
			continue
		}

		for i := range reviewPRs {
			if reviewPRs[i].UpdatedAt.Before(staleThreshold) {
				continue // Skip stale PRs
			}
			incoming = append(incoming, reviewPRs[i])
		}
	}

	return incoming, outgoing
}

// searchPRs executes a GitHub search query and returns matching PRs.
func (f *Fetcher) searchPRs(ctx context.Context, query string) ([]PR, error) {
	opts := &github.SearchOptions{
		Sort:  "updated",
		Order: "desc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var result *github.IssuesSearchResult
	err := retry.Do(
		func() error {
			var resp *github.Response
			var err error
			result, resp, err = f.githubClient.Search.Issues(ctx, query, opts)
			if err != nil {
				// Check if it's a rate limit error
				if resp != nil && resp.StatusCode == http.StatusForbidden {
					slog.Warn("GitHub API rate limited, will retry", "query", query)
					return err // Retry on rate limit
				}
				// Other errors are unrecoverable
				return retry.Unrecoverable(err)
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.Context(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("GitHub search failed after retries: %w", err)
	}

	var prs []PR
	for _, issue := range result.Issues {
		if issue.PullRequestLinks == nil {
			continue // Not a PR
		}

		// Extract owner/repo from repository URL
		// Example: "https://api.github.com/repos/owner/repo" → "owner/repo"
		repoURL := issue.GetRepositoryURL()
		parts := strings.Split(repoURL, "/repos/")
		if len(parts) != 2 {
			continue // Skip if URL format unexpected
		}
		repo := parts[1]

		pr := PR{
			Number:     issue.GetNumber(),
			Title:      issue.GetTitle(),
			URL:        issue.GetHTMLURL(),
			Repository: repo,
			Author:     issue.GetUser().GetLogin(),
			UpdatedAt:  issue.GetUpdatedAt().Time,
			IsDraft:    false, // Draft status is on PullRequest, not Issue
		}

		// Check state store for last event time
		// Split "owner/repo" into owner and repo
		repoParts := strings.SplitN(repo, "/", 2)
		if len(repoParts) != 2 {
			continue // Skip malformed repo
		}
		owner, repoName := repoParts[0], repoParts[1]
		if threadInfo, exists := f.stateStore.GetThread(owner, repoName, pr.Number, ""); exists {
			pr.LastEventTime = threadInfo.LastEventTime
		}

		// If no event time, use UpdatedAt
		if pr.LastEventTime.IsZero() {
			pr.LastEventTime = pr.UpdatedAt
		}

		prs = append(prs, pr)
	}

	return prs, nil
}

// enrichPRs enriches PRs with turnclient analysis.
func (f *Fetcher) enrichPRs(ctx context.Context, prs []PR, githubUsername string, incoming bool) []PR {
	enriched := make([]PR, 0, len(prs))

	turnClient, err := turn.NewDefaultClient()
	if err != nil {
		slog.Warn("failed to create turnclient", "error", err)
		return prs // Return unenriched
	}
	turnClient.SetAuthToken(f.githubToken)

	for i := range prs {
		pr := prs[i]

		// Call turnclient with last event time for cache optimization, with retry
		var checkResult *turn.CheckResponse
		err := retry.Do(
			func() error {
				var err error
				checkResult, err = turnClient.Check(ctx, pr.URL, f.botUsername, pr.LastEventTime)
				return err
			},
			retry.Attempts(3), // Fewer attempts for per-PR enrichment
			retry.Delay(500*time.Millisecond),
			retry.MaxDelay(30*time.Second),
			retry.DelayType(retry.BackOffDelay),
			retry.MaxJitter(time.Second),
			retry.Context(ctx),
		)
		if err != nil {
			slog.Debug("turnclient check failed after retries, using basic PR data",
				"pr", pr.URL,
				"error", err)
			enriched = append(enriched, pr)
			continue
		}

		// Extract action for this user
		if action, exists := checkResult.Analysis.NextAction[githubUsername]; exists {
			pr.ActionReason = action.Reason
			pr.ActionKind = string(action.Kind)

			if incoming {
				pr.NeedsReview = action.Critical
			} else {
				pr.IsBlocked = action.Critical
			}
		}

		// Extract test state from Analysis
		checks := checkResult.Analysis.Checks
		switch {
		case checks.Failing > 0:
			pr.TestState = "failing"
		case checks.Pending > 0 || checks.Waiting > 0:
			pr.TestState = "running"
		case checks.Passing > 0:
			pr.TestState = "passing"
		default:
			// No test state information available
		}

		enriched = append(enriched, pr)
	}

	return enriched
}

// sortPRs sorts PRs with blocked first, then by recency.
func sortPRs(prs []PR) {
	// Simple bubble sort - fine for small lists
	for i := 0; i < len(prs); i++ {
		for j := i + 1; j < len(prs); j++ {
			iBlocked := prs[i].IsBlocked || prs[i].NeedsReview
			jBlocked := prs[j].IsBlocked || prs[j].NeedsReview

			// Determine if we should swap: blocked items first, then by recency
			shouldSwap := (!iBlocked && jBlocked) ||
				(iBlocked == jBlocked && prs[i].UpdatedAt.Before(prs[j].UpdatedAt))

			if shouldSwap {
				prs[i], prs[j] = prs[j], prs[i]
			}
		}
	}
}
