package github

import (
	"context"

	"github.com/google/go-github/v50/github"
)

// ManagerInterface defines the interface for GitHub installation management.
// This interface enables testing of code that depends on Manager.
type ManagerInterface interface {
	// AllOrgs returns all configured organizations.
	AllOrgs() []string

	// ClientForOrg returns the GitHub client for a specific organization.
	ClientForOrg(org string) (ClientInterface, bool)
}

// ClientInterface defines the interface for GitHub API operations.
// This interface enables testing of code that depends on Client.
type ClientInterface interface {
	// Client returns the underlying go-github client for advanced operations.
	Client() *github.Client

	// InstallationToken returns the current installation token for authentication.
	InstallationToken(ctx context.Context) string
}

// Ensure Manager implements ManagerInterface (compile-time check).
var _ ManagerInterface = (*managerWrapper)(nil)

// Ensure Client implements ClientInterface (compile-time check).
var _ ClientInterface = (*Client)(nil)

// managerWrapper wraps Manager to implement ManagerInterface.
// This adapter allows Manager to return ClientInterface instead of *Client.
type managerWrapper struct {
	*Manager
}

// ClientForOrg returns the GitHub client for a specific organization.
// Returns ClientInterface to satisfy the interface contract.
func (m *managerWrapper) ClientForOrg(org string) (ClientInterface, bool) {
	client, ok := m.Manager.ClientForOrg(org)
	if !ok {
		return nil, false
	}
	return client, true
}

// WrapManager wraps a Manager to implement ManagerInterface.
// This is needed because Manager.ClientForOrg returns *Client,
// but ManagerInterface.ClientForOrg must return ClientInterface.
func WrapManager(m *Manager) ManagerInterface {
	return &managerWrapper{Manager: m}
}
