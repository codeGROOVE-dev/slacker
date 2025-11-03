package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"
)

// TestClient_AuthenticateWithMock tests successful authentication flow with mock server.
func TestClient_AuthenticateWithMock(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Add test installation
	mock.AddInstallation(12345, "test-org", "Organization")

	// Generate valid RSA key for JWT
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Create client with baseURL pointing to mock server
	client := &Client{
		appID:          "123456",
		installationID: 12345,
		privateKey:     privateKey,
		baseURL:        mock.URL(),
	}

	// Test authentication
	ctx := context.Background()
	err = client.authenticate(ctx)
	if err != nil {
		t.Fatalf("authenticate() failed: %v", err)
	}

	// Verify requests were made
	if mock.AuthRequests == 0 {
		t.Error("expected authentication request, got none")
	}

	// Verify token was set
	if client.installationToken == "" {
		t.Error("expected installation token to be set")
	}

	// Verify token expiry was set
	if client.tokenExpiry.IsZero() {
		t.Error("expected token expiry to be set")
	}
}

// TestClient_Authenticate_InvalidInstallation tests authentication with invalid installation ID.
func TestClient_Authenticate_InvalidInstallation(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Don't add any installations - mock will return 404

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	client := &Client{
		appID:          "123456",
		installationID: 99999, // Non-existent installation
		privateKey:     privateKey,
		baseURL:        mock.URL(),
	}

	ctx := context.Background()
	err = client.authenticate(ctx)
	if err == nil {
		t.Error("expected error for invalid installation, got nil")
	}
}

// TestClient_Authenticate_RetryOnFailure tests retry logic on transient failures.
func TestClient_Authenticate_RetryOnFailure(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Add installation
	mock.AddInstallation(12345, "test-org", "Organization")

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	client := &Client{
		appID:          "123456",
		installationID: 12345,
		privateKey:     privateKey,
		baseURL:        mock.URL(),
	}

	// Inject failure on first attempt
	mock.FailNextAuthRequest = true

	ctx := context.Background()
	err = client.authenticate(ctx)

	// Should still succeed after retry
	if err != nil {
		t.Fatalf("authenticate() should succeed after retry, got: %v", err)
	}

	// Should have made multiple auth requests
	if mock.AuthRequests < 2 {
		t.Errorf("expected at least 2 auth requests (initial + retry), got %d", mock.AuthRequests)
	}
}

// TestClient_FindPRsForCommit_Success tests finding PRs by commit SHA.
func TestClient_FindPRsForCommit_Success(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Add commit->PR mapping
	mock.AddCommitPRMapping("test-org", "test-repo", "abc123", []int{42, 43})

	// Add the actual PR data
	mock.AddPullRequest("test-org", "test-repo", MockPullRequest{
		Number:    42,
		Title:     "Test PR 1",
		State:     "open",
		HTMLURL:   "https://github.com/test-org/test-repo/pull/42",
		UpdatedAt: time.Now(),
		CreatedAt: time.Now().Add(-24 * time.Hour),
		User:      MockUser{Login: "test-author"},
	})
	mock.AddPullRequest("test-org", "test-repo", MockPullRequest{
		Number:    43,
		Title:     "Test PR 2",
		State:     "open",
		HTMLURL:   "https://github.com/test-org/test-repo/pull/43",
		UpdatedAt: time.Now(),
		CreatedAt: time.Now().Add(-48 * time.Hour),
		User:      MockUser{Login: "test-author"},
	})

	// Create client pointing to mock server
	client := createMockClient(t, mock)

	ctx := context.Background()
	prNumbers, err := client.FindPRsForCommit(ctx, "test-org", "test-repo", "abc123")
	if err != nil {
		t.Fatalf("FindPRsForCommit() failed: %v", err)
	}

	if len(prNumbers) != 2 {
		t.Errorf("expected 2 PRs, got %d", len(prNumbers))
	}

	// Check both PR numbers are present
	found42, found43 := false, false
	for _, num := range prNumbers {
		if num == 42 {
			found42 = true
		}
		if num == 43 {
			found43 = true
		}
	}

	if !found42 || !found43 {
		t.Errorf("expected PR numbers 42 and 43, got %v", prNumbers)
	}
}

// TestClient_FindPRsForCommit_InvalidParams tests error handling for invalid parameters.
func TestClient_FindPRsForCommit_InvalidParams(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	client := createMockClient(t, mock)
	ctx := context.Background()

	tests := []struct {
		name  string
		owner string
		repo  string
		sha   string
	}{
		{"empty owner", "", "repo", "sha"},
		{"empty repo", "owner", "", "sha"},
		{"empty SHA", "owner", "repo", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.FindPRsForCommit(ctx, tt.owner, tt.repo, tt.sha)
			if err == nil {
				t.Error("expected error for invalid params, got nil")
			}
		})
	}
}

// TestClient_FindPRsForCommit_NoResults tests handling when no PRs are found.
func TestClient_FindPRsForCommit_NoResults(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	client := createMockClient(t, mock)
	ctx := context.Background()

	// Query for commit with no PRs
	prNumbers, err := client.FindPRsForCommit(ctx, "test-org", "test-repo", "nonexistent")
	if err != nil {
		t.Fatalf("FindPRsForCommit() failed: %v", err)
	}

	if len(prNumbers) != 0 {
		t.Errorf("expected 0 PRs for nonexistent commit, got %d", len(prNumbers))
	}
}

// TestClient_FindPRsForCommit_OnlyOpenPRs tests that only open PRs are returned.
func TestClient_FindPRsForCommit_OnlyOpenPRs(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Add commit mapping to multiple PRs with different states
	mock.AddCommitPRMapping("test-org", "test-repo", "abc123", []int{1, 2, 3})

	mock.AddPullRequest("test-org", "test-repo", MockPullRequest{
		Number: 1,
		State:  "open",
		Title:  "Open PR",
	})
	mock.AddPullRequest("test-org", "test-repo", MockPullRequest{
		Number: 2,
		State:  "closed",
		Title:  "Closed PR",
	})
	mock.AddPullRequest("test-org", "test-repo", MockPullRequest{
		Number: 3,
		State:  "open",
		Title:  "Another Open PR",
	})

	client := createMockClient(t, mock)
	ctx := context.Background()

	prNumbers, err := client.FindPRsForCommit(ctx, "test-org", "test-repo", "abc123")
	if err != nil {
		t.Fatalf("FindPRsForCommit() failed: %v", err)
	}

	// Should only return the 2 open PRs (1 and 3), not the closed one (2)
	if len(prNumbers) != 2 {
		t.Errorf("expected 2 open PRs, got %d", len(prNumbers))
	}

	for _, num := range prNumbers {
		if num == 2 {
			t.Error("closed PR should not be returned")
		}
	}
}

// createMockClient creates a GitHub client pointed at the mock server.
func createMockClient(t *testing.T, mock *MockGitHubServer) *Client {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Add installation to mock
	mock.AddInstallation(12345, "test-org", "Organization")

	client := &Client{
		appID:          "123456",
		installationID: 12345,
		privateKey:     privateKey,
		organization:   "test-org",
		baseURL:        mock.URL(),
	}

	// Authenticate the client to initialize the GitHub API client
	ctx := context.Background()
	err = client.authenticate(ctx)
	if err != nil {
		t.Fatalf("failed to authenticate mock client: %v", err)
	}

	return client
}
