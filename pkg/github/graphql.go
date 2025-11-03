package github

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/google/go-github/v50/github"
	"golang.org/x/oauth2"
)

// PRSnapshot contains minimal PR information from search query.
type PRSnapshot struct {
	UpdatedAt time.Time
	CreatedAt time.Time
	Owner     string
	Repo      string
	Title     string
	Author    string
	URL       string
	State     string // "OPEN", "CLOSED", "MERGED"
	Number    int
	IsDraft   bool
}

// SearchClient wraps the GitHub Search API client for querying PRs.
type SearchClient struct {
	client *github.Client
}

// NewSearchClient creates a new search client with the given token.
func NewSearchClient(ctx context.Context, token string) *SearchClient {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(ctx, src)
	httpClient.Transport = &userAgentTransport{base: httpClient.Transport}

	return &SearchClient{
		client: github.NewClient(httpClient),
	}
}

// ListOpenPRs queries all open PRs for an organization updated in the last N hours.
// Uses GitHub Search API which is simpler and more reliable than GraphQL.
func (c *SearchClient) ListOpenPRs(ctx context.Context, org string, updatedSinceHours int) ([]PRSnapshot, error) {
	since := time.Now().Add(-time.Duration(updatedSinceHours) * time.Hour)

	// Build search query: "is:pr is:open org:X updated:>YYYY-MM-DD"
	query := fmt.Sprintf("is:pr is:open org:%s updated:>%s",
		org,
		since.Format("2006-01-02"))

	return c.searchPRs(ctx, query, org)
}

// ListClosedPRs queries all closed/merged PRs for an organization updated in the last N hours.
// This is used to update Slack threads when PRs are closed or merged.
func (c *SearchClient) ListClosedPRs(ctx context.Context, org string, updatedSinceHours int) ([]PRSnapshot, error) {
	since := time.Now().Add(-time.Duration(updatedSinceHours) * time.Hour)

	// Build search query: "is:pr is:closed org:X updated:>=YYYY-MM-DD"
	// Use >= instead of > to include PRs closed/merged on the since date
	query := fmt.Sprintf("is:pr is:closed org:%s updated:>=%s",
		org,
		since.Format("2006-01-02"))

	snapshots, err := c.searchPRs(ctx, query, org)
	if err != nil {
		return nil, err
	}

	// Filter by UpdatedAt since GitHub search only has date granularity
	var filtered []PRSnapshot
	for i := range snapshots {
		if snapshots[i].UpdatedAt.Before(since) {
			slog.Debug("filtered out closed PR - updated before window",
				"pr", fmt.Sprintf("%s/%s#%d", snapshots[i].Owner, snapshots[i].Repo, snapshots[i].Number),
				"pr_updated_at", snapshots[i].UpdatedAt,
				"window_start", since,
				"reason", "outside_time_window")
			continue
		}
		filtered = append(filtered, snapshots[i])
	}

	return filtered, nil
}

// searchPRs performs the actual GitHub search query with pagination.
func (c *SearchClient) searchPRs(ctx context.Context, query, org string) ([]PRSnapshot, error) {
	slog.Debug("searching PRs via REST API",
		"org", org,
		"query", query)

	opts := &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 100},
		Sort:        "updated",
		Order:       "desc",
	}

	var allPRs []PRSnapshot
	pageCount := 0
	const maxPages = 10 // Safety limit

	for {
		pageCount++
		if pageCount > maxPages {
			slog.Warn("reached max page limit for search query",
				"org", org,
				"pages", pageCount,
				"prs_collected", len(allPRs))
			break
		}

		result, resp, err := c.client.Search.Issues(ctx, query, opts)
		if err != nil {
			return nil, fmt.Errorf("search query failed: %w", err)
		}

		slog.Debug("search page retrieved",
			"org", org,
			"page", pageCount,
			"results_in_page", len(result.Issues),
			"total_collected", len(allPRs))

		// Process results
		for i := range result.Issues {
			issue := result.Issues[i]

			// Skip if not a PR (issues endpoint returns both issues and PRs)
			if issue.PullRequestLinks == nil {
				continue
			}

			// Extract owner/repo from repository URL
			owner, repo := extractOwnerRepo(issue.GetRepositoryURL())
			if owner == "" || repo == "" {
				slog.Warn("failed to parse repository URL",
					"url", issue.GetRepositoryURL(),
					"issue", issue.GetNumber())
				continue
			}

			// Determine state - for closed PRs, check if merged
			state := "OPEN"
			if issue.GetState() == "closed" {
				// For closed issues, we need to check if it was merged
				// The search API doesn't provide this directly, so we mark as CLOSED
				// The caller can query individual PRs if they need merged status
				state = "CLOSED"
			}

			// Note: Draft status is not reliably available in search API results
			// We set to false since search typically only returns non-draft PRs
			allPRs = append(allPRs, PRSnapshot{
				Owner:     owner,
				Repo:      repo,
				Number:    issue.GetNumber(),
				Title:     issue.GetTitle(),
				Author:    issue.GetUser().GetLogin(),
				URL:       issue.GetHTMLURL(),
				UpdatedAt: issue.GetUpdatedAt().Time,
				CreatedAt: issue.GetCreatedAt().Time,
				State:     state,
				IsDraft:   false, // Search API doesn't reliably provide draft status
			})
		}

		// Check for next page
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	slog.Info("search query complete",
		"org", org,
		"total_prs", len(allPRs),
		"pages_fetched", pageCount,
		"query", query)

	return allPRs, nil
}

// extractOwnerRepo extracts owner and repo from a repository URL.
// Example: "https://api.github.com/repos/owner/repo" -> "owner", "repo"
func extractOwnerRepo(repoURL string) (owner, repo string) {
	// URL format: https://api.github.com/repos/owner/repo
	const prefix = "https://api.github.com/repos/"
	if len(repoURL) <= len(prefix) {
		return "", ""
	}

	path := repoURL[len(prefix):]
	// Split on first slash to get owner/repo
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			return path[:i], path[i+1:]
		}
	}
	return "", ""
}

// TurnClient is an interface for PR analysis.
type TurnClient interface {
	Check(ctx context.Context, prURL, username string, eventTime time.Time) (*turn.CheckResponse, error)
}

// NewTurnClient creates a turnclient with the given token.
func NewTurnClient(token string) (TurnClient, error) {
	tc, err := turn.NewDefaultClient()
	if err != nil {
		return nil, err
	}
	tc.SetAuthToken(token)
	return tc, nil
}

// GraphQLClient is a deprecated alias for SearchClient for backwards compatibility.
// Use SearchClient instead.
type GraphQLClient = SearchClient

// NewGraphQLClient creates a new search client (deprecated name).
// Use NewSearchClient instead.
func NewGraphQLClient(ctx context.Context, token string) *GraphQLClient {
	return NewSearchClient(ctx, token)
}
