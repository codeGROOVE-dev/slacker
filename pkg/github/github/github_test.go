package github

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-github/v50/github"
)

// mockGitHubClient is a simple mock for testing.
type mockGitHubClient struct {
	installationToken string
	client            *github.Client
}

func (m *mockGitHubClient) Client() *github.Client {
	return m.client
}

func (m *mockGitHubClient) InstallationToken(ctx context.Context) string {
	return m.installationToken
}

func TestClient_Client(t *testing.T) {
	ghClient := github.NewClient(nil)
	c := &Client{
		client: ghClient,
	}

	result := c.Client()
	if result != ghClient {
		t.Error("expected Client() to return the underlying github client")
	}
}

func TestClient_InstallationToken(t *testing.T) {
	c := &Client{
		installationToken: "test-token",
		tokenExpiry:       time.Now().Add(1 * time.Hour),
	}

	ctx := context.Background()
	token := c.InstallationToken(ctx)

	if token != "test-token" {
		t.Errorf("expected token 'test-token', got %q", token)
	}
}

func TestClient_InstallationToken_NotExpired(t *testing.T) {
	c := &Client{
		installationToken: "valid-token",
		tokenExpiry:       time.Now().Add(1 * time.Hour), // Not expired
	}

	ctx := context.Background()
	token := c.InstallationToken(ctx)

	// Should return the existing token if not expired
	if token != "valid-token" {
		t.Errorf("expected token 'valid-token', got %q", token)
	}
}

func TestWrapManager(t *testing.T) {
	m := &Manager{
		clients: map[string]*Client{
			"org1": {},
			"org2": {},
		},
	}

	wrapped := WrapManager(m)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapped manager")
	}

	// Test AllOrgs
	orgs := wrapped.AllOrgs()
	if len(orgs) != 2 {
		t.Errorf("expected 2 orgs, got %d", len(orgs))
	}

	// Test ClientForOrg with non-existent org
	_, ok := wrapped.ClientForOrg("nonexistent")
	if ok {
		t.Error("expected ClientForOrg to return false for non-existent org")
	}
}

func TestManagerWrapper_ClientForOrg(t *testing.T) {
	client := &Client{
		organization:      "testorg",
		installationToken: "test-token",
	}

	m := &Manager{
		clients: map[string]*Client{
			"testorg": client,
		},
	}

	wrapped := WrapManager(m)

	// Test with existing org
	gotClient, ok := wrapped.ClientForOrg("testorg")
	if !ok {
		t.Fatal("expected ClientForOrg to return true for existing org")
	}
	if gotClient == nil {
		t.Fatal("expected non-nil client")
	}

	// Verify it's the right client
	if gotClient.(*Client).organization != "testorg" {
		t.Errorf("expected organization 'testorg', got %q", gotClient.(*Client).organization)
	}
}

func TestManager_AllOrgs(t *testing.T) {
	m := &Manager{
		clients: map[string]*Client{
			"org1": {},
			"org2": {},
			"org3": {},
		},
	}

	orgs := m.AllOrgs()

	if len(orgs) != 3 {
		t.Fatalf("expected 3 orgs, got %d", len(orgs))
	}

	expected := map[string]bool{"org1": true, "org2": true, "org3": true}
	for _, org := range orgs {
		if !expected[org] {
			t.Errorf("unexpected org: %s", org)
		}
	}
}

func TestManager_ClientForOrg(t *testing.T) {
	client1 := &Client{organization: "org1"}
	client2 := &Client{organization: "org2"}

	m := &Manager{
		clients: map[string]*Client{
			"org1": client1,
			"org2": client2,
		},
	}

	// Test existing org
	gotClient, ok := m.ClientForOrg("org1")
	if !ok {
		t.Error("expected ClientForOrg to return true for existing org")
	}
	if gotClient != client1 {
		t.Error("expected to get client1")
	}

	// Test non-existent org
	_, ok = m.ClientForOrg("org3")
	if ok {
		t.Error("expected ClientForOrg to return false for non-existent org")
	}
}

func TestRefreshingTokenSource_Token(t *testing.T) {
	c := &Client{
		installationToken: "fresh-token",
		tokenExpiry:       time.Now().Add(1 * time.Hour),
	}

	ts := &refreshingTokenSource{client: c}
	token, err := ts.Token()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if token.AccessToken != "fresh-token" {
		t.Errorf("expected token 'fresh-token', got %q", token.AccessToken)
	}
}

func TestRefreshingTokenSource_Token_ValidToken(t *testing.T) {
	c := &Client{
		installationToken: "another-valid-token",
		tokenExpiry:       time.Now().Add(1 * time.Hour), // Valid token
	}

	ts := &refreshingTokenSource{client: c}
	token, err := ts.Token()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if token.AccessToken != "another-valid-token" {
		t.Errorf("expected token 'another-valid-token', got %q", token.AccessToken)
	}
}
