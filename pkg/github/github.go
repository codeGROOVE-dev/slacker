package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/google/go-github/v50/github"
	"golang.org/x/oauth2"
)

// Client wraps the GitHub API client.
type Client struct {
	appID          string
	privateKey     *rsa.PrivateKey
	installationID int64
	client         *github.Client
}

// New creates a new GitHub client configured as a GitHub App.
func New(ctx context.Context, appID, privateKeyPEM, installationID string) (*Client, error) {
	// Parse the private key.
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 format.
		keyInterface, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		var ok bool
		key, ok = keyInterface.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
	}

	// Parse installation ID.
	instID, err := strconv.ParseInt(installationID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid installation ID: %w", err)
	}

	gc := &Client{
		appID:          appID,
		privateKey:     key,
		installationID: instID,
	}

	// Create authenticated client.
	if err := gc.authenticate(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}

	return gc, nil
}

// authenticate creates an authenticated GitHub client with retry logic.
func (c *Client) authenticate(ctx context.Context) error {
	slog.Info("authenticating GitHub App", "app_id", c.appID)

	// Create JWT for app authentication.
	jwt, err := c.createJWT()
	if err != nil {
		return fmt.Errorf("failed to create JWT: %w", err)
	}

	// Create app client.
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	tc := oauth2.NewClient(ctx, ts)
	appClient := github.NewClient(tc)

	// Get installation token with retry.
	var token *github.InstallationToken
	err = retry.Do(
		func() error {
			var err error
			token, _, err = appClient.Apps.CreateInstallationToken(
				ctx,
				c.installationID,
				&github.InstallationTokenOptions{},
			)
			if err != nil {
				slog.Warn("failed to create installation token, retrying", "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to create installation token after retries: %w", err)
	}

	// Create installation client.
	ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token.GetToken()})
	tc = oauth2.NewClient(ctx, ts)
	c.client = github.NewClient(tc)

	slog.Info("successfully authenticated GitHub App", "app_id", c.appID)
	return nil
}

// createJWT creates a JWT for GitHub App authentication.
func (c *Client) createJWT() (string, error) {
	// This is a simplified version. In production, use a proper JWT library.
	// For now, return a placeholder that would be replaced with actual JWT generation.
	return "jwt-placeholder", nil
}

// GetPR gets pull request details with retry logic.
func (c *Client) GetPR(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	slog.Info("fetching PR", "owner", owner, "repo", repo, "number", number)

	var pr *github.PullRequest
	var resp *github.Response

	err := retry.Do(
		func() error {
			var err error
			pr, resp, err = c.client.PullRequests.Get(ctx, owner, repo, number)
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusNotFound {
					// Don't retry on 404
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to get PR, retrying",
					"owner", owner, "repo", repo, "number", number, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR after retries: %w", err)
	}
	return pr, nil
}

// GetPRReviews gets reviews for a pull request with retry logic.
func (c *Client) GetPRReviews(ctx context.Context, owner, repo string, number int) ([]*github.PullRequestReview, error) {
	slog.Info("fetching PR reviews", "owner", owner, "repo", repo, "number", number)

	var reviews []*github.PullRequestReview

	err := retry.Do(
		func() error {
			var err error
			reviews, _, err = c.client.PullRequests.ListReviews(ctx, owner, repo, number, nil)
			if err != nil {
				slog.Warn("failed to get reviews, retrying",
					"owner", owner, "repo", repo, "number", number, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Error("failed to get PR reviews after retries, returning empty list",
			"owner", owner, "repo", repo, "number", number, "error", err)
		return []*github.PullRequestReview{}, nil // Graceful degradation
	}
	return reviews, nil
}

// GetPRChecks gets check runs for a pull request with retry logic.
func (c *Client) GetPRChecks(ctx context.Context, owner, repo string, number int) (*github.ListCheckRunsResults, error) {
	slog.Info("fetching PR checks", "owner", owner, "repo", repo, "number", number)

	pr, err := c.GetPR(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}

	var checkRuns *github.ListCheckRunsResults

	err = retry.Do(
		func() error {
			var err error
			checkRuns, _, err = c.client.Checks.ListCheckRunsForRef(
				ctx,
				owner,
				repo,
				pr.GetHead().GetSHA(),
				&github.ListCheckRunsOptions{},
			)
			if err != nil {
				slog.Warn("failed to get checks, retrying",
					"owner", owner, "repo", repo, "number", number, "error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Error("failed to get check runs after retries, returning nil",
			"owner", owner, "repo", repo, "number", number, "error", err)
		return nil, nil // Graceful degradation
	}

	return checkRuns, nil
}

// GetPRState determines the current state of a PR.
func (c *Client) GetPRState(ctx context.Context, owner, repo string, number int) (string, []string, error) {
	pr, err := c.GetPR(ctx, owner, repo, number)
	if err != nil {
		return "", nil, err
	}

	// Check if merged or closed.
	if pr.GetMerged() {
		return "pray", nil, nil // Merged
	}
	if pr.GetState() == "closed" {
		return "face_palm", nil, nil // Closed but not merged
	}

	// Get check runs.
	checks, err := c.GetPRChecks(ctx, owner, repo, number)
	if err != nil {
		slog.Warn("failed to get checks for PR state",
			"owner", owner, "repo", repo, "number", number, "error", err)
	}

	// Analyze check status.
	var checksRunning, checksFailed bool
	if checks != nil {
		for _, check := range checks.CheckRuns {
			switch check.GetStatus() {
			case "in_progress", "queued", "pending":
				checksRunning = true
			case "completed":
				if check.GetConclusion() != "success" && check.GetConclusion() != "skipped" {
					checksFailed = true
				}
			}
		}
	}

	// Get reviews.
	reviews, err := c.GetPRReviews(ctx, owner, repo, number)
	if err != nil {
		slog.Warn("failed to get reviews for PR state",
			"owner", owner, "repo", repo, "number", number, "error", err)
	}

	// Check review status.
	hasApproval := false
	needsChanges := false
	reviewers := make(map[string]bool)
	for _, review := range reviews {
		if review.GetUser() != nil {
			reviewers[review.GetUser().GetLogin()] = true
		}
		switch review.GetState() {
		case "APPROVED":
			hasApproval = true
		case "CHANGES_REQUESTED":
			needsChanges = true
		}
	}

	// Determine state and who it's blocked on.
	var state string
	var blockedOn []string

	if checksRunning {
		state = "test_tube" // Tests running
	} else if checksFailed {
		state = "broken_heart"                        // Tests broken
		blockedOn = []string{pr.GetUser().GetLogin()} // Blocked on author
	} else if needsChanges {
		state = "carpentry_saw"                       // Needs changes
		blockedOn = []string{pr.GetUser().GetLogin()} // Blocked on author
	} else if hasApproval {
		state = "check"                               // Approved
		blockedOn = []string{pr.GetUser().GetLogin()} // Author can merge
	} else {
		state = "hourglass" // Waiting for review
		// Get requested reviewers.
		for _, reviewer := range pr.RequestedReviewers {
			blockedOn = append(blockedOn, reviewer.GetLogin())
		}
		for _, team := range pr.RequestedTeams {
			blockedOn = append(blockedOn, "team:"+team.GetSlug())
		}
	}

	return state, blockedOn, nil
}

// WebhookHandler handles GitHub webhooks.
func (c *Client) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	payload, err := github.ValidatePayload(r, []byte("")) // Would use webhook secret here
	if err != nil {
		slog.Warn("failed to validate webhook payload", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		slog.Warn("failed to parse webhook", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch evt := event.(type) {
	case *github.PullRequestEvent:
		slog.Info("PR webhook event",
			"repo", evt.GetRepo().GetFullName(),
			"number", evt.GetNumber(),
			"action", evt.GetAction())
	case *github.PullRequestReviewEvent:
		slog.Info("PR review webhook event",
			"repo", evt.GetRepo().GetFullName(),
			"number", evt.GetPullRequest().GetNumber())
	}

	w.WriteHeader(http.StatusOK)
}

// PRInfo contains simplified PR information.
type PRInfo struct {
	Owner     string
	Repo      string
	Number    int
	Title     string
	Author    string
	State     string
	BlockedOn []string
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GetClient returns the underlying GitHub client.
func (c *Client) GetClient() *github.Client {
	return c.client
}
