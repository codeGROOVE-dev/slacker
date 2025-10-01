// Package secrets provides integration with Google Secret Manager for fetching configuration.
package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/option"
)

// Manager handles fetching secrets from Google Secret Manager.
type Manager struct {
	client    *secretmanager.Client
	projectID string
}

// New creates a new secrets manager with optional credentials.
// If credentialsPath is empty, it uses Application Default Credentials.
func New(ctx context.Context, projectID, credentialsPath string) (*Manager, error) {
	var opts []option.ClientOption
	if credentialsPath != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsPath))
	}

	client, err := secretmanager.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret manager client: %w", err)
	}

	return &Manager{
		client:    client,
		projectID: projectID,
	}, nil
}

// GetWithEnvOverride fetches a secret value from Google Secret Manager,
// but returns the environment variable value if it exists (env vars take precedence).
// The secretName should be the same as the environment variable name (e.g., "SLACK_BOT_TOKEN").
func (m *Manager) GetWithEnvOverride(ctx context.Context, envVar, secretName string) (string, error) {
	// Check environment variable first (takes precedence)
	if value := os.Getenv(envVar); value != "" {
		slog.Info("using environment variable instead of secret",
			"env_var", envVar,
			"source", "environment",
			"has_value", true)
		return value, nil
	}

	// Build the resource name for Secret Manager
	resourceName := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", m.projectID, secretName)

	slog.Info("attempting to access secret from Secret Manager",
		"env_var", envVar,
		"secret_name", secretName,
		"project_id", m.projectID,
		"full_resource_name", resourceName)

	// Access the secret version
	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: resourceName,
	}

	result, err := m.client.AccessSecretVersion(ctx, req)
	if err != nil {
		slog.Error("failed to access secret from Secret Manager",
			"env_var", envVar,
			"secret_name", secretName,
			"resource_name", resourceName,
			"error", err,
			"error_type", fmt.Sprintf("%T", err))
		return "", fmt.Errorf("failed to access secret %s: %w", resourceName, err)
	}

	secretValue := string(result.GetPayload().GetData())
	slog.Info("successfully fetched secret from Google Secret Manager",
		"env_var", envVar,
		"secret_name", secretName,
		"source", "secret_manager",
		"has_value", secretValue != "",
		"value_length", len(secretValue))
	return secretValue, nil
}

// Close closes the Secret Manager client connection.
func (m *Manager) Close() error {
	if m.client != nil {
		return m.client.Close()
	}
	return nil
}
