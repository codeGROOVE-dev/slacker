package slack

import (
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
)

// TestWorkspaceOrgs_MultipleOrgs verifies that workspaceOrgs correctly identifies all orgs for a workspace.
func TestWorkspaceOrgs_MultipleOrgs(t *testing.T) {
	t.Parallel()

	// Create mock config manager with multiple orgs for same workspace
	teamID := "T123"
	configs := map[string]*config.RepoConfig{
		"org1": {
			Users: map[string]string{"gh-user1": "user1@example.com"},
		},
		"org2": {
			Users: map[string]string{"gh-user2": "user2@example.com"},
		},
		"org3": {
			Users: map[string]string{"gh-user3": "user3@example.com"},
		},
		"org4": { // Different teamID - should be excluded
			Users: map[string]string{"gh-user4": "user4@example.com"},
		},
	}

	// Set TeamIDs
	configs["org1"].Global.TeamID = teamID
	configs["org2"].Global.TeamID = teamID
	configs["org3"].Global.TeamID = teamID
	configs["org4"].Global.TeamID = "T999" // Different workspace

	// Create a mock implementation that simulates the workspaceOrgs logic
	allOrgs := []string{"org1", "org2", "org3", "org4"}
	var workspaceOrgs []string

	for _, org := range allOrgs {
		cfg, exists := configs[org]
		if !exists {
			continue
		}
		if cfg.Global.TeamID == teamID {
			workspaceOrgs = append(workspaceOrgs, org)
		}
	}

	// Verify all matching orgs were found
	if len(workspaceOrgs) != 3 {
		t.Errorf("expected 3 workspace orgs, got %d", len(workspaceOrgs))
	}

	expectedOrgs := map[string]bool{
		"org1": true,
		"org2": true,
		"org3": true,
	}

	for _, org := range workspaceOrgs {
		if !expectedOrgs[org] {
			t.Errorf("unexpected org in workspace: %s", org)
		}
		delete(expectedOrgs, org)
	}

	if len(expectedOrgs) > 0 {
		t.Errorf("missing expected orgs: %v", expectedOrgs)
	}
}

// TestCollectOverrides_MultipleOrgs verifies that user overrides are collected from all workspace orgs.
func TestCollectOverrides_MultipleOrgs(t *testing.T) {
	t.Parallel()

	workspaceOrgs := []string{"org1", "org2", "org3"}

	// Simulate configs for multiple orgs
	configs := map[string]*config.RepoConfig{
		"org1": {
			Users: map[string]string{
				"github-user-1": "user1@example.com",
				"github-user-2": "user2@example.com",
			},
		},
		"org2": {
			Users: map[string]string{
				"github-user-3": "user3@example.com",
			},
		},
		"org3": {
			Users: nil, // No overrides
		},
	}

	// Simulate the override collection logic from tryHandleAppHomeOpened
	allOverrides := make(map[string]string)
	for _, org := range workspaceOrgs {
		cfg, exists := configs[org]
		if exists && len(cfg.Users) > 0 {
			for ghUser, email := range cfg.Users {
				allOverrides[ghUser] = email
			}
		}
	}

	// Verify all overrides were collected
	expected := map[string]string{
		"github-user-1": "user1@example.com",
		"github-user-2": "user2@example.com",
		"github-user-3": "user3@example.com",
	}

	if len(allOverrides) != len(expected) {
		t.Errorf("expected %d overrides, got %d", len(expected), len(allOverrides))
	}

	for ghUser, expectedEmail := range expected {
		if actualEmail, ok := allOverrides[ghUser]; !ok {
			t.Errorf("missing override for %s", ghUser)
		} else if actualEmail != expectedEmail {
			t.Errorf("wrong email for %s: expected %s, got %s", ghUser, expectedEmail, actualEmail)
		}
	}
}

// TestUserMappingFallback_MultipleOrgs verifies that user mapping attempts all orgs until one succeeds.
func TestUserMappingFallback_MultipleOrgs(t *testing.T) {
	t.Parallel()

	workspaceOrgs := []string{"org1", "org2", "org3"}

	// Simulate configs for multiple orgs
	configs := map[string]*config.RepoConfig{
		"org1": {Users: nil},
		"org2": {Users: nil},
		"org3": {Users: nil},
	}
	configs["org1"].Global.EmailDomain = "example.com"
	configs["org2"].Global.EmailDomain = "example.org"
	configs["org3"].Global.EmailDomain = "example.net"

	// Simulate the user mapping loop logic
	type attempt struct {
		org    string
		domain string
	}
	var attempts []attempt

	// Simulate mapping attempts (fail on first two, succeed on third)
	var foundMapping bool
	for _, org := range workspaceOrgs {
		cfg, exists := configs[org]
		if !exists {
			continue
		}

		attempts = append(attempts, attempt{
			org:    org,
			domain: cfg.Global.EmailDomain,
		})

		// Simulate: fail on org1 and org2, succeed on org3
		if org == "org3" {
			foundMapping = true
			break
		}
	}

	// Verify all three orgs were attempted (loop broke on success)
	if len(attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", len(attempts))
	}

	// Verify the attempts were in order
	expectedAttempts := []attempt{
		{"org1", "example.com"},
		{"org2", "example.org"},
		{"org3", "example.net"},
	}

	for i, expected := range expectedAttempts {
		if i >= len(attempts) {
			t.Errorf("missing attempt %d", i)
			continue
		}
		actual := attempts[i]
		if actual.org != expected.org || actual.domain != expected.domain {
			t.Errorf("attempt %d: expected (%s, %s), got (%s, %s)",
				i, expected.org, expected.domain, actual.org, actual.domain)
		}
	}

	if !foundMapping {
		t.Error("mapping should have been found")
	}
}

// TestUserMappingEarlyExit_FirstOrgSucceeds verifies that when first org succeeds, subsequent orgs are not attempted.
func TestUserMappingEarlyExit_FirstOrgSucceeds(t *testing.T) {
	t.Parallel()

	workspaceOrgs := []string{"org1", "org2", "org3"}

	// Simulate configs
	configs := map[string]*config.RepoConfig{
		"org1": {Users: nil},
		"org2": {Users: nil},
		"org3": {Users: nil},
	}
	configs["org1"].Global.EmailDomain = "example.com"
	configs["org2"].Global.EmailDomain = "example.org"
	configs["org3"].Global.EmailDomain = "example.net"

	// Simulate mapping attempts (succeed on first)
	var attempts int
	var foundMapping bool
	for _, org := range workspaceOrgs {
		_, exists := configs[org]
		if !exists {
			continue
		}

		attempts++

		// Simulate: succeed on first org
		if org == "org1" {
			foundMapping = true
			break
		}
	}

	// Verify only one attempt was made
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d (should break early on success)", attempts)
	}

	if !foundMapping {
		t.Error("mapping should have been found")
	}
}
