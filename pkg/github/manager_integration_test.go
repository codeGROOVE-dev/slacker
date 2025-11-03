package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
)

// TestManager_RefreshInstallationsWithMock tests successful installation discovery.
func TestManager_RefreshInstallationsWithMock(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Add multiple installations
	mock.AddInstallation(1001, "org1", "Organization")
	mock.AddInstallation(1002, "org2", "Organization")
	mock.AddInstallation(1003, "personal-user", "User")

	// Generate RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Create manager with baseURL pointing to mock
	manager := &Manager{
		appID:                 "123456",
		privateKey:            privateKey,
		clients:               make(map[string]*Client),
		allowPersonalAccounts: false, // Should skip personal accounts
		baseURL:               mock.URL(),
	}

	ctx := context.Background()
	err = manager.RefreshInstallations(ctx)
	if err != nil {
		t.Fatalf("RefreshInstallations() failed: %v", err)
	}

	// Should have 2 organizations (personal account skipped)
	if len(manager.clients) != 2 {
		t.Errorf("expected 2 clients (orgs only), got %d", len(manager.clients))
	}

	// Check that org1 and org2 are present
	if _, ok := manager.clients["org1"]; !ok {
		t.Error("expected org1 client to be created")
	}
	if _, ok := manager.clients["org2"]; !ok {
		t.Error("expected org2 client to be created")
	}
	if _, ok := manager.clients["personal-user"]; ok {
		t.Error("personal account should be skipped when allowPersonalAccounts=false")
	}
}

// TestManager_RefreshInstallations_AllowPersonalAccounts tests personal account handling.
func TestManager_RefreshInstallations_AllowPersonalAccounts(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Add org and personal account
	mock.AddInstallation(1001, "org1", "Organization")
	mock.AddInstallation(1002, "personal-user", "User")

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	manager := &Manager{
		appID:                 "123456",
		privateKey:            privateKey,
		clients:               make(map[string]*Client),
		allowPersonalAccounts: true, // Should include personal accounts
		baseURL:               mock.URL(),
	}

	ctx := context.Background()
	err = manager.RefreshInstallations(ctx)
	if err != nil {
		t.Fatalf("RefreshInstallations() failed: %v", err)
	}

	// Should have both org and personal account
	if len(manager.clients) != 2 {
		t.Errorf("expected 2 clients (org + user), got %d", len(manager.clients))
	}

	if _, ok := manager.clients["org1"]; !ok {
		t.Error("expected org1 client to be created")
	}
	if _, ok := manager.clients["personal-user"]; !ok {
		t.Error("expected personal-user client to be created when allowPersonalAccounts=true")
	}
}

// TestManager_RefreshInstallations_NoInstallations tests handling of no installations.
func TestManager_RefreshInstallations_NoInstallations(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Don't add any installations

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	manager := &Manager{
		appID:      "123456",
		privateKey: privateKey,
		clients:    make(map[string]*Client),
		baseURL:    mock.URL(),
	}

	ctx := context.Background()
	err = manager.RefreshInstallations(ctx)
	if err != nil {
		t.Fatalf("RefreshInstallations() should succeed with no installations, got error: %v", err)
	}

	if len(manager.clients) != 0 {
		t.Errorf("expected 0 clients with no installations, got %d", len(manager.clients))
	}
}

// TestManager_RefreshInstallations_PreserveExisting tests that existing clients are preserved.
func TestManager_RefreshInstallations_PreserveExisting(t *testing.T) {
	mock := NewMockGitHubServer()
	defer mock.Close()

	// Add installation
	mock.AddInstallation(1001, "org1", "Organization")

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	manager := &Manager{
		appID:      "123456",
		privateKey: privateKey,
		clients:    make(map[string]*Client),
		baseURL:    mock.URL(),
	}

	// Create a mock client manually to test preservation
	existingClient := &Client{
		appID:          "123456",
		installationID: 1001,
		organization:   "org1",
	}
	manager.clients["org1"] = existingClient

	ctx := context.Background()
	err = manager.RefreshInstallations(ctx)
	if err != nil {
		t.Fatalf("RefreshInstallations() failed: %v", err)
	}

	// Should still have org1 client (either preserved or refreshed)
	if _, ok := manager.clients["org1"]; !ok {
		t.Error("expected org1 client to be present after refresh")
	}
}
