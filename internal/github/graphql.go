package github

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	"github.com/google/go-github/v50/github"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

// PRSnapshot contains minimal PR information from GraphQL query.
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

// GraphQLClient wraps the GitHub GraphQL API client.
type GraphQLClient struct {
	client *githubv4.Client
	v3     *github.Client // Fallback to REST API
}

// NewGraphQLClient creates a new GraphQL client with the given token.
func NewGraphQLClient(ctx context.Context, token string) *GraphQLClient {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(ctx, src)
	httpClient.Transport = &userAgentTransport{base: httpClient.Transport}

	return &GraphQLClient{
		client: githubv4.NewClient(httpClient),
		v3:     github.NewClient(httpClient),
	}
}

// ListOpenPRs queries all open PRs for an organization updated in the last N hours.
// Uses GraphQL for efficiency (single query vs many REST calls).
// Falls back to REST API if GraphQL fails.
func (c *GraphQLClient) ListOpenPRs(ctx context.Context, org string, updatedSinceHours int) ([]PRSnapshot, error) {
	// Try GraphQL first (efficient)
	prs, err := c.listOpenPRsGraphQL(ctx, org, updatedSinceHours)
	if err != nil {
		slog.Warn("GraphQL query failed, falling back to REST API",
			"org", org,
			"error", err)
		// Fall back to REST API (slower but more reliable)
		return c.listOpenPRsREST(ctx, org, updatedSinceHours)
	}

	return prs, nil
}

// ListClosedPRs queries all closed/merged PRs for an organization updated in the last N hours.
// This is used to update Slack threads when PRs are closed or merged.
func (c *GraphQLClient) ListClosedPRs(ctx context.Context, org string, updatedSinceHours int) ([]PRSnapshot, error) {
	since := time.Now().Add(-time.Duration(updatedSinceHours) * time.Hour)

	// GraphQL query structure
	//nolint:govet // Inline anonymous struct matches GraphQL API structure for clarity
	var query struct {
		Search struct {
			Nodes []struct {
				PullRequest struct {
					UpdatedAt time.Time
					CreatedAt time.Time
					Title     string
					URL       string
					State     string
					Number    int
					IsDraft   bool
					Merged    bool
					Author    struct {
						Login string
					}
					Repository struct {
						Name  string
						Owner struct {
							Login string
						}
					}
				} `graphql:"... on PullRequest"`
			}
			PageInfo struct {
				EndCursor   string
				HasNextPage bool
			}
		} `graphql:"search(query: $searchQuery, type: ISSUE, first: 100, after: $cursor)"`
	}

	// Build search query: "is:pr is:closed org:X updated:>=YYYY-MM-DD"
	// Use >= instead of > to include PRs closed/merged on the since date
	// Note: GitHub search uses date-only granularity, so we need >= to catch PRs from today
	searchQuery := fmt.Sprintf("is:pr is:closed org:%s updated:>=%s",
		org,
		since.Format("2006-01-02"))

	variables := map[string]any{
		"searchQuery": githubv4.String(searchQuery),
		"cursor":      (*githubv4.String)(nil),
	}

	var allPRs []PRSnapshot
	pageCount := 0
	const maxPages = 10

	for {
		pageCount++
		if pageCount > maxPages {
			slog.Warn("reached max page limit for closed PR GraphQL query",
				"org", org,
				"pages", pageCount,
				"prs_collected", len(allPRs))
			break
		}

		err := c.client.Query(ctx, &query, variables)
		if err != nil {
			return nil, fmt.Errorf("GraphQL query failed: %w", err)
		}

		// Process this page of results
		for i := range query.Search.Nodes {
			pr := query.Search.Nodes[i].PullRequest

			// Filter by UpdatedAt since GitHub search only has date granularity
			if pr.UpdatedAt.Before(since) {
				slog.Debug("filtered out closed PR - updated before window",
					"pr", fmt.Sprintf("%s/%s#%d", pr.Repository.Owner.Login, pr.Repository.Name, pr.Number),
					"pr_updated_at", pr.UpdatedAt,
					"window_start", since,
					"reason", "outside_time_window")
				continue
			}

			// Determine state: MERGED takes precedence over CLOSED
			state := "CLOSED"
			if pr.Merged {
				state = "MERGED"
			}

			allPRs = append(allPRs, PRSnapshot{
				Owner:     pr.Repository.Owner.Login,
				Repo:      pr.Repository.Name,
				Number:    pr.Number,
				Title:     pr.Title,
				Author:    pr.Author.Login,
				URL:       pr.URL,
				UpdatedAt: pr.UpdatedAt,
				CreatedAt: pr.CreatedAt,
				State:     state,
				IsDraft:   pr.IsDraft,
			})
		}

		if !query.Search.PageInfo.HasNextPage {
			break
		}

		cursor := githubv4.String(query.Search.PageInfo.EndCursor)
		variables["cursor"] = cursor
	}

	slog.Info("GraphQL query for closed PRs complete",
		"org", org,
		"total_prs", len(allPRs),
		"pages_fetched", pageCount,
		"query", searchQuery,
		"time_window_start", since.Format(time.RFC3339))

	return allPRs, nil
}

// listOpenPRsGraphQL queries using GraphQL for efficiency.
func (c *GraphQLClient) listOpenPRsGraphQL(ctx context.Context, org string, updatedSinceHours int) ([]PRSnapshot, error) {
	slog.Debug("querying open PRs via GraphQL",
		"org", org,
		"updated_since_hours", updatedSinceHours)

	// Calculate the timestamp for filtering
	since := time.Now().Add(-time.Duration(updatedSinceHours) * time.Hour)

	// GraphQL query structure
	//nolint:govet // Inline anonymous struct matches GraphQL API structure for clarity
	var query struct {
		Search struct {
			Nodes []struct {
				PullRequest struct {
					UpdatedAt time.Time
					CreatedAt time.Time
					Title     string
					URL       string
					State     string
					Number    int
					IsDraft   bool
					Author    struct {
						Login string
					}
					Repository struct {
						Name  string
						Owner struct {
							Login string
						}
					}
				} `graphql:"... on PullRequest"`
			}
			PageInfo struct {
				EndCursor   string
				HasNextPage bool
			}
		} `graphql:"search(query: $searchQuery, type: ISSUE, first: 100, after: $cursor)"`
	}

	// Build search query: "is:pr is:open org:X updated:>YYYY-MM-DD"
	searchQuery := fmt.Sprintf("is:pr is:open org:%s updated:>%s",
		org,
		since.Format("2006-01-02"))

	variables := map[string]any{
		"searchQuery": githubv4.String(searchQuery),
		"cursor":      (*githubv4.String)(nil), // Start with no cursor
	}

	var allPRs []PRSnapshot
	pageCount := 0
	const maxPages = 10 // Safety limit to prevent infinite loops

	for {
		pageCount++
		if pageCount > maxPages {
			slog.Warn("reached max page limit for GraphQL query",
				"org", org,
				"pages", pageCount,
				"prs_collected", len(allPRs))
			break
		}

		err := c.client.Query(ctx, &query, variables)
		if err != nil {
			return nil, fmt.Errorf("GraphQL query failed: %w", err)
		}

		slog.Debug("GraphQL page retrieved",
			"org", org,
			"page", pageCount,
			"results_in_page", len(query.Search.Nodes),
			"total_collected", len(allPRs))

		// Process this page of results
		for i := range query.Search.Nodes {
			pr := query.Search.Nodes[i].PullRequest
			allPRs = append(allPRs, PRSnapshot{
				Owner:     pr.Repository.Owner.Login,
				Repo:      pr.Repository.Name,
				Number:    pr.Number,
				Title:     pr.Title,
				Author:    pr.Author.Login,
				URL:       pr.URL,
				UpdatedAt: pr.UpdatedAt,
				CreatedAt: pr.CreatedAt,
				State:     pr.State,
				IsDraft:   pr.IsDraft,
			})
		}

		// Check if there are more pages
		if !query.Search.PageInfo.HasNextPage {
			break
		}

		// Update cursor for next page
		cursor := githubv4.String(query.Search.PageInfo.EndCursor)
		variables["cursor"] = cursor
	}

	slog.Info("GraphQL query complete",
		"org", org,
		"total_prs", len(allPRs),
		"pages_fetched", pageCount,
		"query", searchQuery)

	return allPRs, nil
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

// listOpenPRsREST queries using REST API as fallback.
// Less efficient but more reliable than GraphQL.
func (c *GraphQLClient) listOpenPRsREST(ctx context.Context, org string, updatedSinceHours int) ([]PRSnapshot, error) {
	slog.Info("querying open PRs via REST API (GraphQL fallback)",
		"org", org,
		"updated_since_hours", updatedSinceHours)

	since := time.Now().Add(-time.Duration(updatedSinceHours) * time.Hour)

	// List all repos in the org first
	opts := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	var allPRs []PRSnapshot
	repoCount := 0

	for {
		repos, resp, err := c.v3.Repositories.ListByOrg(ctx, org, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repos: %w", err)
		}

		repoCount += len(repos)

		// For each repo, get open PRs
		for _, repo := range repos {
			repoName := repo.GetName()

			prOpts := &github.PullRequestListOptions{
				State:       "open",
				ListOptions: github.ListOptions{PerPage: 100},
			}

			for {
				prs, prResp, err := c.v3.PullRequests.List(ctx, org, repoName, prOpts)
				if err != nil {
					slog.Warn("failed to list PRs for repo, skipping",
						"org", org,
						"repo", repoName,
						"error", err)
					break
				}

				for _, pr := range prs {
					// Filter by updated time
					if pr.GetUpdatedAt().Before(since) {
						continue
					}

					allPRs = append(allPRs, PRSnapshot{
						Owner:     org,
						Repo:      repoName,
						Number:    pr.GetNumber(),
						Title:     pr.GetTitle(),
						Author:    pr.GetUser().GetLogin(),
						URL:       pr.GetHTMLURL(),
						UpdatedAt: pr.GetUpdatedAt().Time,
						CreatedAt: pr.GetCreatedAt().Time,
						State:     pr.GetState(),
						IsDraft:   pr.GetDraft(),
					})
				}

				if prResp.NextPage == 0 {
					break
				}
				prOpts.Page = prResp.NextPage
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	slog.Info("REST API query complete",
		"org", org,
		"repos_scanned", repoCount,
		"total_prs", len(allPRs))

	return allPRs, nil
}
