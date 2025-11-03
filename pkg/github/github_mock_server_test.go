package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v50/github"
)

// MockGitHubServer provides a test HTTP server that mocks GitHub API endpoints.
// It handles authentication, installation management, and PR queries.
type MockGitHubServer struct {
	server        *httptest.Server
	installations []MockInstallation
	pullRequests  map[string][]MockPullRequest // key: "owner/repo"
	commits       map[string][]MockCommit      // key: "owner/repo/sha"

	// Request tracking for assertions
	AuthRequests         int
	InstallationRequests int
	PRRequests           int

	// Failure injection for testing retry logic
	FailNextAuthRequest bool
}

// MockInstallation represents a GitHub App installation.
type MockInstallation struct {
	ID      int64
	Account MockAccount
}

// MockAccount represents a GitHub account (org or user).
type MockAccount struct {
	Login string
	Type  string // "Organization" or "User"
}

// MockPullRequest represents a PR for testing.
type MockPullRequest struct {
	Number    int
	Title     string
	State     string // "open", "closed"
	HTMLURL   string
	UpdatedAt time.Time
	CreatedAt time.Time
	User      MockUser
	HeadSHA   string
}

// MockUser represents a GitHub user.
type MockUser struct {
	Login string
}

// MockCommit represents a commit for PR lookup.
type MockCommit struct {
	SHA string
	PRs []int // PR numbers associated with this commit
}

// NewMockGitHubServer creates a new mock GitHub API server.
// Returns the server and its base URL for use in tests.
func NewMockGitHubServer() *MockGitHubServer {
	mock := &MockGitHubServer{
		installations: []MockInstallation{},
		pullRequests:  make(map[string][]MockPullRequest),
		commits:       make(map[string][]MockCommit),
	}

	// Create HTTP server with router
	mux := http.NewServeMux()

	// GitHub App authentication endpoints
	mux.HandleFunc("/app/installations", mock.handleListInstallations)
	mux.HandleFunc("/app/installations/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			// POST /app/installations/{id}/access_tokens
			mock.handleCreateInstallationToken(w, r)
		} else {
			// GET /app/installations/{id}
			mock.handleGetInstallation(w, r)
		}
	})

	// Installation token creation (also handle /installations/{id}/access_tokens for compatibility)
	mux.HandleFunc("/installations/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			mock.handleCreateInstallationToken(w, r)
		} else {
			// GET /app/installations/{id}
			mock.handleGetInstallation(w, r)
		}
	})

	// Pull request endpoints
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/commits/") && strings.Contains(r.URL.Path, "/pulls") {
			mock.handleListPRsForCommit(w, r)
		} else if strings.Contains(r.URL.Path, "/pulls") {
			mock.handleListPRs(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	// Search API
	mux.HandleFunc("/search/issues", mock.handleSearchIssues)

	// Rate limit endpoint for token validation
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resources": map[string]interface{}{
				"core": map[string]interface{}{
					"limit":     5000,
					"remaining": 5000,
					"reset":     time.Now().Add(1 * time.Hour).Unix(),
				},
			},
		})
	})

	// Installation repositories endpoint for token validation
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"total_count":  0,
			"repositories": []interface{}{},
		})
	})

	mock.server = httptest.NewServer(mux)
	return mock
}

// URL returns the base URL of the mock server.
func (m *MockGitHubServer) URL() string {
	return m.server.URL
}

// Close shuts down the mock server.
func (m *MockGitHubServer) Close() {
	m.server.Close()
}

// AddInstallation adds a mock installation to the server.
func (m *MockGitHubServer) AddInstallation(id int64, orgLogin, accountType string) {
	m.installations = append(m.installations, MockInstallation{
		ID: id,
		Account: MockAccount{
			Login: orgLogin,
			Type:  accountType,
		},
	})
}

// AddPullRequest adds a mock PR to a repository.
func (m *MockGitHubServer) AddPullRequest(owner, repo string, pr MockPullRequest) {
	key := owner + "/" + repo
	m.pullRequests[key] = append(m.pullRequests[key], pr)
}

// AddCommitPRMapping adds a mapping from commit SHA to PR numbers.
func (m *MockGitHubServer) AddCommitPRMapping(owner, repo, sha string, prNumbers []int) {
	key := owner + "/" + repo + "/" + sha
	m.commits[key] = []MockCommit{{SHA: sha, PRs: prNumbers}}
}

// handleListInstallations handles GET /app/installations.
func (m *MockGitHubServer) handleListInstallations(w http.ResponseWriter, r *http.Request) {
	m.InstallationRequests++

	// Check for valid JWT in Authorization header
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, `{"message": "Bad credentials"}`, http.StatusUnauthorized)
		return
	}

	// Return installations
	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
	_ = json.NewEncoder(w).Encode(m.installations)
}

// handleGetInstallation handles GET /app/installations/{id}.
func (m *MockGitHubServer) handleGetInstallation(w http.ResponseWriter, r *http.Request) {
	// Extract installation ID from path
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}

	idStr := parts[3]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"message": "Invalid installation ID"}`, http.StatusBadRequest)
		return
	}

	// Find installation
	for _, inst := range m.installations {
		if inst.ID == id {
			w.Header().Set("Content-Type", "application/json")
			//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
			_ = json.NewEncoder(w).Encode(inst)
			return
		}
	}

	http.Error(w, `{"message": "Not Found"}`, http.StatusNotFound)
}

// handleCreateInstallationToken handles POST /installations/{id}/access_tokens.
func (m *MockGitHubServer) handleCreateInstallationToken(w http.ResponseWriter, r *http.Request) {
	m.AuthRequests++

	// Inject failure for retry testing
	if m.FailNextAuthRequest {
		m.FailNextAuthRequest = false // Only fail once
		http.Error(w, `{"message": "Service temporarily unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	// Extract installation ID
	// Path is either /app/installations/{id}/access_tokens or /installations/{id}/access_tokens
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, `{"message": "Invalid path"}`, http.StatusBadRequest)
		return
	}

	// Find the installation ID - it's the part before "access_tokens"
	var idStr string
	for i, part := range parts {
		if part == "access_tokens" && i > 0 {
			idStr = parts[i-1]
			break
		}
	}

	if idStr == "" {
		http.Error(w, `{"message": "Installation ID not found in path"}`, http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, `{"message": "Invalid installation ID"}`, http.StatusBadRequest)
		return
	}

	// Check if installation exists
	found := false
	for _, inst := range m.installations {
		if inst.ID == id {
			found = true
			break
		}
	}

	if !found {
		http.Error(w, `{"message": "Not Found"}`, http.StatusNotFound)
		return
	}

	// Return installation token
	token := &github.InstallationToken{
		Token:     github.String("ghs_mock_installation_token_" + idStr),
		ExpiresAt: &github.Timestamp{Time: time.Now().Add(1 * time.Hour)},
	}

	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
	_ = json.NewEncoder(w).Encode(token)
}

// handleListPRsForCommit handles GET /repos/{owner}/{repo}/commits/{sha}/pulls.
func (m *MockGitHubServer) handleListPRsForCommit(w http.ResponseWriter, r *http.Request) {
	m.PRRequests++

	// Parse path: /repos/{owner}/{repo}/commits/{sha}/pulls
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}

	owner := parts[0]
	repo := parts[1]
	sha := parts[3]

	key := owner + "/" + repo + "/" + sha

	// Find PRs for this commit
	var prs []*github.PullRequest
	if commits, ok := m.commits[key]; ok {
		for _, commit := range commits {
			for _, prNum := range commit.PRs {
				// Find the PR details
				repoKey := owner + "/" + repo
				if repoPRs, ok := m.pullRequests[repoKey]; ok {
					for _, pr := range repoPRs {
						if pr.Number == prNum {
							prs = append(prs, &github.PullRequest{
								Number:    github.Int(pr.Number),
								Title:     github.String(pr.Title),
								State:     github.String(pr.State),
								HTMLURL:   github.String(pr.HTMLURL),
								UpdatedAt: &github.Timestamp{Time: pr.UpdatedAt},
								CreatedAt: &github.Timestamp{Time: pr.CreatedAt},
								User:      &github.User{Login: github.String(pr.User.Login)},
							})
							break
						}
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
	_ = json.NewEncoder(w).Encode(prs)
}

// handleListPRs handles GET /repos/{owner}/{repo}/pulls.
func (m *MockGitHubServer) handleListPRs(w http.ResponseWriter, r *http.Request) {
	m.PRRequests++

	// Parse path: /repos/{owner}/{repo}/pulls
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	owner := parts[0]
	repo := parts[1]
	key := owner + "/" + repo

	// Get state filter from query params
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "open"
	}

	// Filter PRs by state
	var prs []*github.PullRequest
	if repoPRs, ok := m.pullRequests[key]; ok {
		for _, pr := range repoPRs {
			if state == "all" || pr.State == state {
				prs = append(prs, &github.PullRequest{
					Number:    github.Int(pr.Number),
					Title:     github.String(pr.Title),
					State:     github.String(pr.State),
					HTMLURL:   github.String(pr.HTMLURL),
					UpdatedAt: &github.Timestamp{Time: pr.UpdatedAt},
					CreatedAt: &github.Timestamp{Time: pr.CreatedAt},
					User:      &github.User{Login: github.String(pr.User.Login)},
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
	_ = json.NewEncoder(w).Encode(prs)
}

// handleSearchIssues handles GET /search/issues (used for PR search).
func (m *MockGitHubServer) handleSearchIssues(w http.ResponseWriter, r *http.Request) {
	m.PRRequests++

	query := r.URL.Query().Get("q")

	// Simple query parsing - just extract org if present
	var org string
	if strings.Contains(query, "org:") {
		parts := strings.Split(query, " ")
		for _, part := range parts {
			if strings.HasPrefix(part, "org:") {
				org = strings.TrimPrefix(part, "org:")
				break
			}
		}
	}

	// Collect all PRs from repos in this org
	var items []map[string]any
	for repoKey, prs := range m.pullRequests {
		parts := strings.Split(repoKey, "/")
		if len(parts) != 2 {
			continue
		}
		repoOwner, repoName := parts[0], parts[1]

		// Filter by org if specified
		if org != "" && repoOwner != org {
			continue
		}

		for _, pr := range prs {
			// Check state filter in query
			if strings.Contains(query, "is:open") && pr.State != "open" {
				continue
			}

			items = append(items, map[string]any{
				"number":     pr.Number,
				"title":      pr.Title,
				"state":      pr.State,
				"html_url":   pr.HTMLURL,
				"updated_at": pr.UpdatedAt.Format(time.RFC3339),
				"created_at": pr.CreatedAt.Format(time.RFC3339),
				"user":       map[string]any{"login": pr.User.Login},
				"pull_request": map[string]any{
					"url": fmt.Sprintf("%s/repos/%s/%s/pulls/%d", m.server.URL, repoOwner, repoName, pr.Number),
				},
				"repository_url": fmt.Sprintf("%s/repos/%s/%s", m.server.URL, repoOwner, repoName),
			})
		}
	}

	result := map[string]any{
		"total_count":        len(items),
		"incomplete_results": false,
		"items":              items,
	}

	w.Header().Set("Content-Type", "application/json")
	//nolint:errcheck // Error intentionally ignored in test mock HTTP handler
	_ = json.NewEncoder(w).Encode(result)
}
