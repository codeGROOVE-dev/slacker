// Package github provides a GitHub API client for GitHub App interactions.
package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v50/github"
	"golang.org/x/oauth2"
)

// Constants for security requirements.
const (
	minRSAKeyBits = 2048
)

// Client wraps the GitHub API client.
type Client struct {
	tokenExpiry       time.Time
	privateKey        *rsa.PrivateKey
	client            *github.Client
	appID             string
	installationToken string
	organization      string
	installationID    int64
	tokenMutex        sync.RWMutex
}

// refreshingTokenSource implements oauth2.TokenSource that automatically refreshes tokens.
type refreshingTokenSource struct {
	client *Client
}

// Token returns a fresh token, refreshing if necessary.
func (ts *refreshingTokenSource) Token() (*oauth2.Token, error) {
	// Use a background context for token refresh - token operations should complete
	// independently of request contexts to avoid breaking long-running connections
	token := ts.client.InstallationToken(context.Background())
	if token == "" {
		return nil, errors.New("no token available")
	}
	return &oauth2.Token{AccessToken: token}, nil
}

// userAgentTransport adds a custom User-Agent header to requests.
type userAgentTransport struct {
	base http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "Slacker/1.0.0 (github.com/codeGROOVE-dev/slacker)")
	return t.base.RoundTrip(req)
}

// New creates a new GitHub client configured as a GitHub App.
func New(ctx context.Context, appID, privateKeyPEM, installationID string) (*Client, error) {
	// Parse the private key.
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("failed to parse PEM block")
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
			return nil, errors.New("private key is not RSA")
		}
	}

	// Validate RSA key strength (minimum 2048 bits).
	if key.N.BitLen() < minRSAKeyBits {
		return nil, fmt.Errorf("RSA key too weak: %d bits (minimum %d required)", key.N.BitLen(), minRSAKeyBits)
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
	slog.Info("authenticating GitHub App",
		"app_id", c.appID,
		"installation_id", c.installationID)

	// Create JWT for app authentication.
	jwtToken, err := c.createJWT()
	if err != nil {
		slog.Error("failed to create JWT for GitHub App authentication",
			"app_id", c.appID,
			"error", err,
			"hint", "Check that your GitHub private key is valid and in the correct format (PEM)")
		return fmt.Errorf("failed to create JWT: %w", err)
	}

	// Create app client with custom user-agent.
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwtToken})
	tc := oauth2.NewClient(ctx, ts)
	tc.Transport = &userAgentTransport{base: tc.Transport}
	appClient := github.NewClient(tc)

	// Get installation token with retry.
	var token *github.InstallationToken
	err = retry.Do(
		func() error {
			var resp *github.Response
			var err error
			token, resp, err = appClient.Apps.CreateInstallationToken(
				ctx,
				c.installationID,
				&github.InstallationTokenOptions{},
			)
			if err != nil {
				// Provide helpful error messages based on the error type
				if resp != nil {
					switch resp.StatusCode {
					case http.StatusNotFound:
						slog.Error("GitHub App installation not found",
							"installation_id", c.installationID,
							"hint", "Check that GITHUB_INSTALLATION_ID is correct. Find it at: https://github.com/settings/installations")
						return retry.Unrecoverable(fmt.Errorf("installation %d not found", c.installationID))
					case http.StatusForbidden:
						slog.Error("GitHub App lacks permissions",
							"installation_id", c.installationID,
							"hint", "Ensure the GitHub App has been installed and has the necessary permissions")
						return retry.Unrecoverable(err)
					case http.StatusUnauthorized:
						slog.Error("GitHub App authentication failed",
							"app_id", c.appID,
							"hint", "Check that your GitHub App ID and private key are correct")
						return retry.Unrecoverable(err)
					default:
						// Other errors might be transient
						slog.Warn("unexpected status code from GitHub API",
							"status_code", resp.StatusCode,
							"installation_id", c.installationID)
					}
				}
				slog.Warn("failed to create installation token, retrying",
					"error", err,
					"installation_id", c.installationID)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to create installation token after retries: %w", err)
	}

	// Get installation details to find the organization
	installation, _, err := appClient.Apps.GetInstallation(ctx, c.installationID)
	if err != nil {
		slog.Warn("failed to get installation details", "error", err)
		// Don't fail here, we can still work without knowing the org
	} else if installation.Account != nil && installation.Account.Login != nil {
		c.tokenMutex.Lock()
		c.organization = *installation.Account.Login
		c.tokenMutex.Unlock()
		slog.Info("detected organization from installation",
			"organization", c.organization,
			"installation_id", c.installationID)
	}

	// Create installation client with auto-refreshing token source and custom user-agent.
	// The refreshingTokenSource will automatically call InstallationToken() which handles
	// token expiry checking and refreshing.
	ts = &refreshingTokenSource{client: c}
	tc = oauth2.NewClient(ctx, ts)
	tc.Transport = &userAgentTransport{base: tc.Transport}
	c.client = github.NewClient(tc)

	// Store the token with expiry (GitHub tokens expire after 1 hour).
	// For security, refresh every 30 minutes instead of waiting until near expiry.
	c.tokenMutex.Lock()
	c.installationToken = token.GetToken()
	c.tokenExpiry = time.Now().Add(30 * time.Minute) // Refresh every 30 minutes for security
	c.tokenMutex.Unlock()

	// Test the token by making a simple API call
	// Use an endpoint that works with installation tokens
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _, testErr := c.client.Apps.ListRepos(testCtx, nil)
	if testErr != nil {
		// Try a simpler test - just get the rate limit
		_, _, testErr = c.client.RateLimits(testCtx)
		if testErr != nil {
			slog.Warn("token validation test failed", "error", testErr)
		} else {
			slog.Debug("token validated successfully (rate limit check)")
		}
	} else {
		slog.Debug("token validated successfully (repo list check)")
	}

	// Log minimal token info to reduce exposure in logs (security best practice)
	tokenStr := token.GetToken()
	tokenSuffix := "..."
	if len(tokenStr) >= 4 {
		tokenSuffix = "..." + tokenStr[len(tokenStr)-4:]
	}
	slog.Info("successfully authenticated GitHub App",
		"app_id", c.appID,
		"token_length", len(tokenStr),
		"token_suffix", tokenSuffix,
		"token_expires_at", token.GetExpiresAt())
	return nil
}

// createJWT creates a JWT for GitHub App authentication.
func (c *Client) createJWT() (string, error) {
	// Create claims with required fields for GitHub App authentication.
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)), // GitHub allows max 10 minutes
		Issuer:    c.appID,
	}

	// Create token with claims.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	// Sign with private key.
	tokenString, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return tokenString, nil
}

// PR gets pull request details with retry logic.
func (c *Client) PR(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	// Validate inputs.
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("invalid owner or repo: owner=%q, repo=%q", owner, repo)
	}
	if number <= 0 {
		return nil, fmt.Errorf("invalid PR number: %d", number)
	}

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
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR after retries: %w", err)
	}
	return pr, nil
}

// PRReviews gets reviews for a pull request with retry logic.
func (c *Client) PRReviews(ctx context.Context, owner, repo string, number int) ([]*github.PullRequestReview, error) {
	// Validate inputs.
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("invalid owner or repo: owner=%q, repo=%q", owner, repo)
	}
	if number <= 0 {
		return nil, fmt.Errorf("invalid PR number: %d", number)
	}

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
		retry.MaxJitter(time.Second),
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

// PRChecks gets check runs for a pull request with retry logic.
func (c *Client) PRChecks(ctx context.Context, owner, repo string, number int) (*github.ListCheckRunsResults, error) {
	// Validate inputs.
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("invalid owner or repo: owner=%q, repo=%q", owner, repo)
	}
	if number <= 0 {
		return nil, fmt.Errorf("invalid PR number: %d", number)
	}

	slog.Info("fetching PR checks", "owner", owner, "repo", repo, "number", number)

	pr, err := c.PR(ctx, owner, repo, number)
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
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Error("failed to get check runs after retries, returning empty result",
			"owner", owner, "repo", repo, "number", number, "error", err)
		// Return an empty result instead of nil for graceful degradation
		return &github.ListCheckRunsResults{
			CheckRuns: []*github.CheckRun{},
		}, nil
	}

	return checkRuns, nil
}

// PRState determines the current state of a PR.
func (c *Client) PRState(ctx context.Context, owner, repo string, number int) (state string, blockedOn []string, err error) {
	pr, err := c.PR(ctx, owner, repo, number)
	if err != nil {
		return "", nil, err
	}

	// Validate PR is not nil
	if pr == nil {
		return "", nil, errors.New("PR is nil")
	}
	// Check if merged or closed.
	if pr.GetMerged() {
		return "merged", nil, nil // Merged
	}
	if pr.GetState() == "closed" {
		return "face_palm", nil, nil // Closed but not merged
	}

	// Get check runs.
	checks, err := c.PRChecks(ctx, owner, repo, number)
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
			default:
				// Unknown check status, log for debugging
				slog.Debug("unknown check status", "status", check.GetStatus())
			}
		}
	}

	// Get reviews.
	reviews, err := c.PRReviews(ctx, owner, repo, number)
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
		default:
			// Other review states (COMMENTED, PENDING, DISMISSED, etc.)
			slog.Debug("other review state", "state", review.GetState())
		}
	}

	// Determine state and who it's blocked on.
	// Priority order: running tests > broken tests > needs changes > approved > waiting
	if checksRunning {
		return "test_tube", nil, nil // Tests running, no one blocked
	}

	author := pr.GetUser().GetLogin()

	if checksFailed {
		return "broken_heart", []string{author}, nil // Tests broken, blocked on author
	}

	if needsChanges {
		return "carpentry_saw", []string{author}, nil // Changes requested, blocked on author
	}

	if hasApproval {
		return "check", []string{author}, nil // Approved, author can merge
	}

	// Waiting for review - collect all requested reviewers
	for _, reviewer := range pr.RequestedReviewers {
		blockedOn = append(blockedOn, reviewer.GetLogin())
	}
	for _, team := range pr.RequestedTeams {
		blockedOn = append(blockedOn, "team:"+team.GetSlug())
	}

	return "hourglass", blockedOn, nil
}

// PRReviewers gets all reviewers (requested and completed) for a PR.
func (c *Client) PRReviewers(ctx context.Context, owner, repo string, number int) ([]string, error) {
	var allReviewers []string
	reviewerSet := make(map[string]bool) // Use set to avoid duplicates

	slog.Debug("fetching PR reviewers",
		"owner", owner,
		"repo", repo,
		"pr_number", number)

	// Get PR details to get requested reviewers
	pr, err := c.PR(ctx, owner, repo, number)
	if err != nil {
		slog.Error("failed to get PR details for reviewers",
			"owner", owner,
			"repo", repo,
			"pr_number", number,
			"error", err)
		return nil, err
	}

	// Add requested reviewers
	for _, reviewer := range pr.RequestedReviewers {
		if reviewer.GetLogin() != "" {
			reviewerSet[reviewer.GetLogin()] = true
		}
	}

	// Get reviews to find completed reviewers
	reviews, err := c.PRReviews(ctx, owner, repo, number)
	if err != nil {
		slog.Warn("failed to get PR reviews, continuing with requested reviewers only",
			"owner", owner,
			"repo", repo,
			"pr_number", number,
			"error", err)
	} else {
		// Add reviewers who have already reviewed
		for _, review := range reviews {
			if review.GetUser() != nil && review.GetUser().GetLogin() != "" {
				reviewerSet[review.GetUser().GetLogin()] = true
			}
		}
	}

	// Convert set to slice
	for reviewer := range reviewerSet {
		allReviewers = append(allReviewers, reviewer)
	}

	slog.Debug("collected PR reviewers",
		"owner", owner,
		"repo", repo,
		"pr_number", number,
		"reviewers", allReviewers,
		"reviewer_count", len(allReviewers))

	return allReviewers, nil
}

// PRInfo contains simplified PR information.
type PRInfo struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	Owner     string
	Repo      string
	Title     string
	Author    string
	State     string
	URL       string
	BlockedOn []string
	Number    int
}

// FindPRsForCommit finds all open PRs associated with a commit SHA.
func (c *Client) FindPRsForCommit(ctx context.Context, owner, repo, sha string) ([]int, error) {
	if owner == "" || repo == "" || sha == "" {
		return nil, fmt.Errorf("invalid parameters: owner=%q, repo=%q, sha=%q", owner, repo, sha)
	}

	slog.Debug("looking up PRs for commit",
		"owner", owner,
		"repo", repo,
		"sha", sha)

	var allPRs []*github.PullRequest
	err := retry.Do(
		func() error {
			var resp *github.Response
			var err error
			allPRs, resp, err = c.client.PullRequests.ListPullRequestsWithCommit(
				ctx,
				owner,
				repo,
				sha,
				&github.PullRequestListOptions{
					State: "all", // Include open, closed, and merged
				},
			)
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusNotFound {
					// Commit doesn't exist or no PRs found - don't retry
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to list PRs for commit, retrying",
					"owner", owner,
					"repo", repo,
					"sha", sha,
					"error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Debug("no PRs found for commit",
			"owner", owner,
			"repo", repo,
			"sha", sha,
			"error", err)
		return []int{}, nil // Return empty list, not error
	}

	// Extract PR numbers from open PRs only
	var prNumbers []int
	for _, pr := range allPRs {
		if pr.GetState() == "open" {
			prNumbers = append(prNumbers, pr.GetNumber())
		}
	}

	slog.Debug("found PRs for commit",
		"owner", owner,
		"repo", repo,
		"sha", sha,
		"pr_count", len(prNumbers),
		"pr_numbers", prNumbers)

	return prNumbers, nil
}

// Client returns the underlying GitHub client.
func (c *Client) Client() *github.Client {
	return c.client
}

// RefreshToken forces a token refresh.
func (c *Client) RefreshToken(ctx context.Context) error {
	slog.Info("forcing GitHub token refresh")
	return c.authenticate(ctx)
}

// Organization returns the organization associated with this installation.
func (c *Client) Organization() string {
	c.tokenMutex.RLock()
	defer c.tokenMutex.RUnlock()
	return c.organization
}

// InstallationToken returns the current installation token, refreshing if needed.
// This method is safe to call concurrently - only one goroutine will perform the refresh.
func (c *Client) InstallationToken(ctx context.Context) string {
	// First check with read lock (fast path for common case)
	c.tokenMutex.RLock()
	token := c.installationToken
	expiry := c.tokenExpiry
	needsRefresh := time.Now().After(expiry)
	c.tokenMutex.RUnlock()

	if !needsRefresh {
		return token
	}

	// Token needs refresh - acquire write lock to coordinate
	c.tokenMutex.Lock()
	// Double-check after acquiring write lock (another goroutine might have refreshed)
	if time.Now().After(c.tokenExpiry) {
		slog.Info("GitHub installation token expired, refreshing",
			"old_token_prefix", c.installationToken[:min(10, len(c.installationToken))]+"...",
			"expiry_was", c.tokenExpiry)

		// Release lock during API call to avoid blocking other operations
		c.tokenMutex.Unlock()

		if err := c.authenticate(ctx); err != nil {
			slog.Error("failed to refresh GitHub token", "error", err)
			// Return old token as fallback (might still work for a bit)
			c.tokenMutex.RLock()
			fallbackToken := c.installationToken
			c.tokenMutex.RUnlock()
			return fallbackToken
		}

		c.tokenMutex.RLock()
		refreshedToken := c.installationToken
		c.tokenMutex.RUnlock()
		slog.Info("GitHub token refreshed successfully",
			"new_token_prefix", refreshedToken[:min(10, len(refreshedToken))]+"...")
		return refreshedToken
	}

	// Another goroutine refreshed while we were waiting for the lock
	token = c.installationToken
	c.tokenMutex.Unlock()
	return token
}

// Manager manages multiple GitHub App installations.
type Manager struct {
	privateKey            *rsa.PrivateKey
	clients               map[string]*Client // org -> client
	appID                 string
	allowPersonalAccounts bool // Allow processing personal accounts (default: false for DoS protection)
	mu                    sync.RWMutex
}

// NewManager creates a new installation manager.
func NewManager(ctx context.Context, appID, privateKeyPEM string, allowPersonalAccounts bool) (*Manager, error) {
	// Parse the private key.
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("failed to parse PEM block")
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
			return nil, errors.New("private key is not RSA")
		}
	}

	// Validate RSA key strength (minimum 2048 bits).
	if key.N.BitLen() < minRSAKeyBits {
		return nil, fmt.Errorf("RSA key too weak: %d bits (minimum %d required)", key.N.BitLen(), minRSAKeyBits)
	}

	m := &Manager{
		clients:               make(map[string]*Client),
		appID:                 appID,
		privateKey:            key,
		allowPersonalAccounts: allowPersonalAccounts,
	}

	// Discover installations at startup.
	if err := m.RefreshInstallations(ctx); err != nil {
		return nil, fmt.Errorf("failed to discover installations: %w", err)
	}

	return m, nil
}

// RefreshInstallations discovers all installations and creates clients for them.
func (m *Manager) RefreshInstallations(ctx context.Context) error {
	slog.Info("discovering GitHub App installations", "app_id", m.appID)

	// Create JWT for app-level authentication.
	jwtToken, err := m.createJWT()
	if err != nil {
		return fmt.Errorf("failed to create JWT: %w", err)
	}

	// Create app client.
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwtToken})
	tc := oauth2.NewClient(ctx, ts)
	tc.Transport = &userAgentTransport{base: tc.Transport}
	appClient := github.NewClient(tc)

	// List all installations with retry.
	var installations []*github.Installation
	err = retry.Do(
		func() error {
			var resp *github.Response
			var err error
			installations, resp, err = appClient.Apps.ListInstallations(ctx, &github.ListOptions{
				PerPage: 100,
			})
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusUnauthorized {
					slog.Error("GitHub App authentication failed",
						"app_id", m.appID,
						"hint", "Check that your GitHub App ID and private key are correct")
					return retry.Unrecoverable(err)
				}
				slog.Warn("failed to list installations, retrying",
					"error", err,
					"app_id", m.appID)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		return fmt.Errorf("failed to list installations after retries: %w", err)
	}

	slog.Info("discovered GitHub App installations",
		"app_id", m.appID,
		"installation_count", len(installations))

	// Create clients for each installation.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Start with existing clients to preserve valid ones if refresh fails
	newClients := make(map[string]*Client)
	for org, client := range m.clients {
		newClients[org] = client
	}

	for _, inst := range installations {
		if inst.Account == nil || inst.Account.Login == nil {
			slog.Warn("installation missing account information",
				"installation_id", inst.GetID())
			continue
		}

		// Skip personal accounts if not explicitly allowed (DoS protection)
		if !m.allowPersonalAccounts && inst.Account.GetType() == "User" {
			slog.Debug("skipping personal account",
				"account", inst.Account.GetLogin(),
				"type", "User")
			continue
		}

		org := inst.Account.GetLogin()

		// Create client for this installation.
		gc := &Client{
			appID:          m.appID,
			privateKey:     m.privateKey,
			installationID: inst.GetID(),
		}

		// Use a timeout context for each org authentication to ensure
		// shutdown doesn't block on hung API calls and to prevent
		// one slow org from blocking others.
		authCtx, authCancel := context.WithTimeout(ctx, 15*time.Second)
		err := gc.authenticate(authCtx)
		authCancel()

		if err != nil {
			// Skip this org but continue with others.
			// Preserve existing client if we have one.
			if errors.Is(err, context.Canceled) {
				slog.Info("authentication canceled during shutdown",
					"org", org,
					"installation_id", inst.GetID())
			} else {
				slog.Error("failed to authenticate installation",
					"org", org,
					"installation_id", inst.GetID(),
					"error", err)
			}
			// Keep existing client if we have one
			if _, hasExisting := m.clients[org]; hasExisting {
				slog.Info("preserving existing client after auth failure",
					"org", org)
			}
			continue
		}

		newClients[org] = gc
		slog.Info("created client for installation",
			"org", org,
			"installation_id", inst.GetID(),
			"account_type", inst.Account.GetType())
	}

	// Only remove clients for orgs that are no longer in the installation list
	discoveredOrgs := make(map[string]bool)
	for _, inst := range installations {
		if inst.Account != nil && inst.Account.Login != nil {
			discoveredOrgs[inst.Account.GetLogin()] = true
		}
	}
	for org := range m.clients {
		if !discoveredOrgs[org] {
			slog.Info("removing client for uninstalled org", "org", org)
			delete(newClients, org)
		}
	}

	// Replace old clients with new ones.
	m.clients = newClients

	slog.Info("installation refresh complete",
		"app_id", m.appID,
		"active_clients", len(m.clients))

	return nil
}

// createJWT creates a JWT for GitHub App authentication.
func (m *Manager) createJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    m.appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return tokenString, nil
}

// ClientForOrg returns the GitHub client for a specific organization.
func (m *Manager) ClientForOrg(org string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, exists := m.clients[org]
	return client, exists
}

// AllOrgs returns a list of all organizations with active installations.
func (m *Manager) AllOrgs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	orgs := make([]string, 0, len(m.clients))
	for org := range m.clients {
		orgs = append(orgs, org)
	}
	return orgs
}
