package home

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/google/go-github/v50/github"
)

// TestNewFetcher verifies constructor initializes all fields.
func TestNewFetcher(t *testing.T) {
	client := &github.Client{}
	token := "test-token"
	botUsername := "test-bot"

	fetcher := NewFetcher(client, nil, token, botUsername)

	if fetcher == nil {
		t.Fatal("expected non-nil fetcher")
	}
	if fetcher.githubClient != client {
		t.Error("expected githubClient to be set")
	}
	if fetcher.githubToken != token {
		t.Error("expected githubToken to be set")
	}
	if fetcher.botUsername != botUsername {
		t.Error("expected botUsername to be set")
	}
}

// TestSortPRs verifies PR sorting logic (blocked first, then by recency).
func TestSortPRs(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		prs      []PR
		expected []int // Expected PR numbers in sorted order
	}{
		{
			name: "blocked PRs come first",
			prs: []PR{
				{Number: 1, UpdatedAt: now.Add(-1 * time.Hour), IsBlocked: false},
				{Number: 2, UpdatedAt: now.Add(-2 * time.Hour), IsBlocked: true},
				{Number: 3, UpdatedAt: now.Add(-30 * time.Minute), IsBlocked: false},
			},
			expected: []int{2, 3, 1}, // Blocked first (2), then by recency (3, 1)
		},
		{
			name: "needs review treated as blocked",
			prs: []PR{
				{Number: 1, UpdatedAt: now.Add(-1 * time.Hour), IsBlocked: false, NeedsReview: false},
				{Number: 2, UpdatedAt: now.Add(-2 * time.Hour), IsBlocked: false, NeedsReview: true},
				{Number: 3, UpdatedAt: now.Add(-30 * time.Minute), IsBlocked: false, NeedsReview: false},
			},
			expected: []int{2, 3, 1}, // NeedsReview first (2), then by recency (3, 1)
		},
		{
			name: "sort by recency when all same blocking status",
			prs: []PR{
				{Number: 1, UpdatedAt: now.Add(-5 * time.Hour), IsBlocked: false},
				{Number: 2, UpdatedAt: now.Add(-1 * time.Hour), IsBlocked: false},
				{Number: 3, UpdatedAt: now.Add(-3 * time.Hour), IsBlocked: false},
			},
			expected: []int{2, 3, 1}, // Most recent first
		},
		{
			name: "multiple blocked - sort by recency",
			prs: []PR{
				{Number: 1, UpdatedAt: now.Add(-1 * time.Hour), IsBlocked: true},
				{Number: 2, UpdatedAt: now.Add(-3 * time.Hour), IsBlocked: true},
				{Number: 3, UpdatedAt: now.Add(-2 * time.Hour), IsBlocked: true},
			},
			expected: []int{1, 3, 2}, // All blocked, so by recency
		},
		{
			name:     "empty list",
			prs:      []PR{},
			expected: []int{},
		},
		{
			name: "single PR",
			prs: []PR{
				{Number: 42, UpdatedAt: now, IsBlocked: false},
			},
			expected: []int{42},
		},
		{
			name: "mixed blocked and needs review",
			prs: []PR{
				{Number: 1, UpdatedAt: now.Add(-1 * time.Hour), IsBlocked: true, NeedsReview: false},
				{Number: 2, UpdatedAt: now.Add(-2 * time.Hour), IsBlocked: false, NeedsReview: true},
				{Number: 3, UpdatedAt: now.Add(-30 * time.Minute), IsBlocked: false, NeedsReview: false},
			},
			expected: []int{1, 2, 3}, // Both blocked/needs review first by recency, then non-blocked by recency
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortPRs(tt.prs)

			if len(tt.prs) != len(tt.expected) {
				t.Fatalf("length mismatch: got %d, want %d", len(tt.prs), len(tt.expected))
			}

			for i, expectedNum := range tt.expected {
				if tt.prs[i].Number != expectedNum {
					t.Errorf("position %d: got PR#%d, want PR#%d", i, tt.prs[i].Number, expectedNum)
				}
			}
		})
	}
}

// TestFetchUserPRs_InputValidation tests input validation for username and org names.
func TestFetchUserPRs_InputValidation(t *testing.T) {
	f := &Fetcher{
		githubClient: &github.Client{},
		stateStore:   nil,
	}

	ctx := context.Background()

	tests := []struct {
		name          string
		username      string
		workspaceOrgs []string
		expectEmpty   bool
	}{
		{
			name:          "empty username returns empty",
			username:      "",
			workspaceOrgs: []string{"test-org"},
			expectEmpty:   true,
		},
		{
			name:          "username with space returns empty",
			username:      "user name",
			workspaceOrgs: []string{"test-org"},
			expectEmpty:   true,
		},
		{
			name:          "username with tab returns empty",
			username:      "user\tname",
			workspaceOrgs: []string{"test-org"},
			expectEmpty:   true,
		},
		{
			name:          "username with newline returns empty",
			username:      "user\nname",
			workspaceOrgs: []string{"test-org"},
			expectEmpty:   true,
		},
		{
			name:          "username with quote returns empty",
			username:      "user\"name",
			workspaceOrgs: []string{"test-org"},
			expectEmpty:   true,
		},
		{
			name:          "org with space is skipped",
			username:      "validuser",
			workspaceOrgs: []string{"invalid org"},
			expectEmpty:   true,
		},
		{
			name:          "empty org list returns empty",
			username:      "validuser",
			workspaceOrgs: []string{},
			expectEmpty:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			incoming, outgoing := f.fetchUserPRs(ctx, tt.username, tt.workspaceOrgs)

			if !tt.expectEmpty {
				t.Skip("test expects non-empty, but would require GitHub API mocking")
			}

			if len(incoming) != 0 {
				t.Errorf("expected empty incoming PRs, got %d", len(incoming))
			}
			if len(outgoing) != 0 {
				t.Errorf("expected empty outgoing PRs, got %d", len(outgoing))
			}
		})
	}
}

// mockStateStore implements state.Store interface for testing.
type mockStateStore struct {
	threads map[string]state.ThreadInfo
}

func (m *mockStateStore) Thread(owner, repo string, number int, channelID string) (state.ThreadInfo, bool) {
	key := owner + "/" + repo + "/" + string(rune(number))
	info, exists := m.threads[key]
	return info, exists
}

func (m *mockStateStore) SaveThread(owner, repo string, number int, channelID string, info state.ThreadInfo) error {
	return nil
}

func (m *mockStateStore) LastDM(userID, prURL string) (time.Time, bool) {
	return time.Time{}, false
}

func (m *mockStateStore) RecordDM(userID, prURL string, sentAt time.Time) error {
	return nil
}

func (m *mockStateStore) DMMessage(userID, prURL string) (state.DMInfo, bool) {
	return state.DMInfo{}, false
}

func (m *mockStateStore) SaveDMMessage(userID, prURL string, info state.DMInfo) error {
	return nil
}

func (m *mockStateStore) ListDMUsers(prURL string) []string {
	return nil
}

func (m *mockStateStore) LastDigest(userID, date string) (time.Time, bool) {
	return time.Time{}, false
}

func (m *mockStateStore) RecordDigest(userID, date string, sentAt time.Time) error {
	return nil
}

func (m *mockStateStore) WasProcessed(eventKey string) bool {
	return false
}

func (m *mockStateStore) MarkProcessed(eventKey string, ttl time.Duration) error {
	return nil
}

func (m *mockStateStore) LastNotification(prURL string) time.Time {
	return time.Time{}
}

func (m *mockStateStore) RecordNotification(prURL string, notifiedAt time.Time) error {
	return nil
}

func (m *mockStateStore) Cleanup() error {
	return nil
}

func (m *mockStateStore) QueuePendingDM(dm state.PendingDM) error {
	return nil
}

func (m *mockStateStore) GetPendingDMs(before time.Time) ([]state.PendingDM, error) {
	return nil, nil
}

func (m *mockStateStore) RemovePendingDM(id string) error {
	return nil
}

func (m *mockStateStore) Close() error {
	return nil
}

// TestSearchPRs tests GitHub search API integration.
func TestSearchPRs(t *testing.T) {
	// Create mock GitHub API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/issues" {
			// Return mock search results
			response := github.IssuesSearchResult{
				Total: github.Int(1),
				Issues: []*github.Issue{
					{
						Number:        github.Int(123),
						Title:         github.String("Test PR"),
						HTMLURL:       github.String("https://github.com/test-org/test-repo/pull/123"),
						RepositoryURL: github.String("https://api.github.com/repos/test-org/test-repo"),
						User: &github.User{
							Login: github.String("testuser"),
						},
						UpdatedAt: &github.Timestamp{Time: time.Now().Add(-1 * time.Hour)},
						PullRequestLinks: &github.PullRequestLinks{
							URL: github.String("https://api.github.com/repos/test-org/test-repo/pulls/123"),
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := github.NewClient(nil)
	client.BaseURL = mustParseURL(server.URL + "/")

	mockStore := &mockStateStore{
		threads: map[string]state.ThreadInfo{
			"test-org/test-repo/\x7b": { // \x7b is ASCII for '{'
				LastEventTime: time.Now().Add(-30 * time.Minute),
			},
		},
	}

	f := &Fetcher{
		githubClient: client,
		stateStore:   mockStore,
	}

	ctx := context.Background()
	prs, err := f.searchPRs(ctx, "is:pr is:open author:testuser org:test-org")
	if err != nil {
		t.Fatalf("searchPRs failed: %v", err)
	}

	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}

	if prs[0].Number != 123 {
		t.Errorf("expected PR #123, got #%d", prs[0].Number)
	}

	if prs[0].Repository != "test-org/test-repo" {
		t.Errorf("expected repo test-org/test-repo, got %s", prs[0].Repository)
	}
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// TestFetchDashboard tests the main dashboard fetching logic.
func TestFetchDashboard(t *testing.T) {
	// Create mock GitHub API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/issues" {
			query := r.URL.Query().Get("q")
			var issues []*github.Issue

			if containsStr(query, "author:testuser") {
				// Outgoing PRs
				issues = []*github.Issue{
					{
						Number:        github.Int(456),
						Title:         github.String("My PR"),
						HTMLURL:       github.String("https://github.com/org/repo/pull/456"),
						RepositoryURL: github.String("https://api.github.com/repos/org/repo"),
						User:          &github.User{Login: github.String("testuser")},
						UpdatedAt:     &github.Timestamp{Time: time.Now().Add(-2 * time.Hour)},
						PullRequestLinks: &github.PullRequestLinks{
							URL: github.String("https://api.github.com/repos/org/repo/pulls/456"),
						},
					},
				}
			} else if containsStr(query, "review-requested:testuser") {
				// Incoming PRs
				issues = []*github.Issue{
					{
						Number:        github.Int(789),
						Title:         github.String("Review needed"),
						HTMLURL:       github.String("https://github.com/org/repo/pull/789"),
						RepositoryURL: github.String("https://api.github.com/repos/org/repo"),
						User:          &github.User{Login: github.String("otheruser")},
						UpdatedAt:     &github.Timestamp{Time: time.Now().Add(-1 * time.Hour)},
						PullRequestLinks: &github.PullRequestLinks{
							URL: github.String("https://api.github.com/repos/org/repo/pulls/789"),
						},
					},
				}
			}

			response := github.IssuesSearchResult{
				Total:  github.Int(len(issues)),
				Issues: issues,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := github.NewClient(nil)
	client.BaseURL = mustParseURL(server.URL + "/")

	f := NewFetcher(client, &mockStateStore{threads: make(map[string]state.ThreadInfo)}, "", "bot")

	ctx := context.Background()
	dashboard, err := f.FetchDashboard(ctx, "testuser", []string{"org"})
	if err != nil {
		t.Fatalf("FetchDashboard failed: %v", err)
	}

	if len(dashboard.IncomingPRs) != 1 {
		t.Errorf("expected 1 incoming PR, got %d", len(dashboard.IncomingPRs))
	}

	if len(dashboard.OutgoingPRs) != 1 {
		t.Errorf("expected 1 outgoing PR, got %d", len(dashboard.OutgoingPRs))
	}

	if len(dashboard.WorkspaceOrgs) != 1 || dashboard.WorkspaceOrgs[0] != "org" {
		t.Errorf("expected workspace orgs [org], got %v", dashboard.WorkspaceOrgs)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && indexOfStr(s, substr) >= 0
}

func indexOfStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
