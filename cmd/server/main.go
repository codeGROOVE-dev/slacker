// Package main implements the slacker server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	"github.com/codeGROOVE-dev/slacker/pkg/secrets"
	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
	"github.com/gorilla/mux"
	"golang.org/x/sync/errgroup"
)

// Server configuration constants.
const (
	serverReadTimeout  = 15 * time.Second
	serverWriteTimeout = 15 * time.Second
)

func main() {
	// Load configuration from environment.
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("received shutdown signal, gracefully stopping")
		cancel()

		// Force exit after 5 seconds if graceful shutdown doesn't complete
		time.Sleep(5 * time.Second)
		slog.Error("graceful shutdown timeout, forcing exit")
		os.Exit(1)
	}()

	// Log configuration without secrets.
	slog.Info("configuration loaded",
		"data_dir", cfg.DataDir,
		"sprinkler_url", cfg.SprinklerURL,
		"github_app_id", cfg.GitHubAppID,
		"github_installation_id", cfg.GitHubInstallationID,
		"has_slack_token", cfg.SlackToken != "",
		"has_slack_signing_secret", cfg.SlackSigningSecret != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "",
		"startup_message", "Starting Slacker server...")

	// Initialize state manager with file persistence.
	stateManager := state.New(cfg.DataDir)

	// Initialize config manager for repo configs.
	configManager := config.New(ctx)

	// Initialize GitHub client.
	githubClient, err := github.New(ctx, cfg.GitHubAppID, cfg.GitHubPrivateKey, cfg.GitHubInstallationID)
	if err != nil {
		slog.Error("failed to initialize GitHub client", "error", err)
		cancel() // Ensure cleanup happens before exit
		return   // Let main return naturally instead of os.Exit
	}

	// Initialize Slack client.
	slackClient := slack.New(cfg.SlackToken, cfg.SlackSigningSecret)

	// Initialize notification manager.
	notifier := notify.New(slackClient, stateManager, configManager)

	// Initialize bot coordinator.
	botCoordinator := bot.New(
		ctx,
		slackClient,
		githubClient,
		stateManager,
		configManager,
		notifier,
		cfg.SprinklerURL,
	)

	// Setup HTTP routes with security middleware.
	router := mux.NewRouter()
	router.Use(securityHeadersMiddleware)
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/slack/events", slackClient.EventsHandler).Methods("POST")
	router.HandleFunc("/slack/interactions", slackClient.InteractionsHandler).Methods("POST")
	router.HandleFunc("/slack/slash", slackClient.SlashCommandHandler).Methods("POST")

	// Determine port.
	port := os.Getenv("PORT")
	if port == "" {
		port = "9119"
	}

	// Start server and bot services.
	eg, ctx := errgroup.WithContext(ctx)

	// HTTP server.
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  60 * time.Second,
	}

	eg.Go(func() error {
		slog.Info("starting server", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	eg.Go(func() error {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown failed: %w", err)
		}
		return nil
	})

	// Start bot coordinator using the sprinkler client library.
	eg.Go(func() error {
		slog.Debug("starting bot coordinator goroutine")
		err := botCoordinator.RunWithSprinklerClient(ctx)
		slog.Debug("bot coordinator goroutine ended", "error", err)
		return err
	})

	// Start notification scheduler.
	eg.Go(func() error {
		slog.Debug("starting notifier goroutine")
		err := notifier.Run(ctx)
		slog.Debug("notifier goroutine ended", "error", err)
		return err
	})

	// Wait for all services.
	slog.Debug("waiting for all services to complete")
	if err := eg.Wait(); err != nil {
		slog.Error("server error", "error", err)
	}
	slog.Info("server stopped")
}

func loadConfig() (*config.ServerConfig, error) {
	ctx := context.Background()

	// Check if Google Secret Manager should be used
	// Try multiple common project ID environment variables
	var secretsManager *secrets.Manager

	// Log all environment variables that might contain project info (for debugging)
	slog.Info("checking for project ID in environment",
		"GCP_PROJECT_ID", os.Getenv("GCP_PROJECT_ID"),
		"GOOGLE_CLOUD_PROJECT", os.Getenv("GOOGLE_CLOUD_PROJECT"),
		"GCP_PROJECT", os.Getenv("GCP_PROJECT"),
		"PROJECT_ID", os.Getenv("PROJECT_ID"),
		"GCLOUD_PROJECT", os.Getenv("GCLOUD_PROJECT"))

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if projectID == "" {
		projectID = os.Getenv("GCP_PROJECT")
	}
	if projectID == "" {
		projectID = os.Getenv("PROJECT_ID")
	}
	if projectID == "" {
		projectID = os.Getenv("GCLOUD_PROJECT")
	}

	// Check if we're running on Cloud Run
	isCloudRun := os.Getenv("K_SERVICE") != "" || os.Getenv("CLOUD_RUN_TIMEOUT_SECONDS") != ""

	slog.Info("Secret Manager configuration",
		"project_id", projectID,
		"has_project", projectID != "",
		"is_cloud_run", isCloudRun,
		"google_application_credentials", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		"k_service", os.Getenv("K_SERVICE"),
		"cloud_run_timeout", os.Getenv("CLOUD_RUN_TIMEOUT_SECONDS"))

	if isCloudRun && projectID == "" {
		slog.Warn("Running on Cloud Run but no project ID found. Set GCP_PROJECT_ID environment variable in your Cloud Run service configuration")
	}

	if projectID != "" {
		// Initialize secrets manager
		credentialsPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		var err error
		slog.Info("attempting to initialize Google Secret Manager",
			"project_id", projectID,
			"credentials_path", credentialsPath)

		secretsManager, err = secrets.New(ctx, projectID, credentialsPath)
		if err != nil {
			slog.Error("failed to initialize Google Secret Manager, falling back to env vars",
				"project_id", projectID,
				"credentials_path", credentialsPath,
				"error", err,
				"error_detail", fmt.Sprintf("%+v", err))
			// Continue without secrets manager
		} else {
			defer func() {
				if err := secretsManager.Close(); err != nil {
					slog.Warn("failed to close secrets manager", "error", err)
				}
			}()
			slog.Info("Google Secret Manager successfully initialized",
				"project_id", projectID,
				"has_credentials", credentialsPath != "")
		}
	}

	// Helper function to get secret values with Secret Manager fallback
	getSecretValue := func(envVar string) string {
		// Environment variable takes precedence
		if value := os.Getenv(envVar); value != "" {
			slog.Info("using environment variable",
				"env_var", envVar,
				"source", "environment")
			return value
		}

		slog.Info("environment variable not found, checking Secret Manager",
			"env_var", envVar,
			"secret_manager_available", secretsManager != nil)

		// Try Secret Manager if available (using same name as env var)
		if secretsManager != nil {
			slog.Info("attempting to fetch from Secret Manager",
				"env_var", envVar,
				"secret_name", envVar)

			value, err := secretsManager.GetWithEnvOverride(ctx, envVar, envVar)
			if err != nil {
				slog.Error("failed to fetch secret from Secret Manager",
					"env_var", envVar,
					"secret_name", envVar,
					"error", err,
					"error_detail", fmt.Sprintf("%+v", err))
				return ""
			}
			slog.Info("successfully fetched secret from Secret Manager",
				"env_var", envVar,
				"has_value", value != "")
			return value
		}

		slog.Warn("Secret Manager not initialized, cannot fetch secret",
			"env_var", envVar)
		return ""
	}

	// Load GitHub private key from environment, file, or Secret Manager
	githubPrivateKey := getSecretValue("GITHUB_PRIVATE_KEY")
	if githubPrivateKey == "" {
		if keyPath := os.Getenv("GITHUB_PRIVATE_KEY_PATH"); keyPath != "" {
			keyData, err := os.ReadFile(keyPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read GitHub private key from %s: %w", keyPath, err)
			}
			githubPrivateKey = string(keyData)
		}
	}

	// Set defaults for optional config
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user config directory: %w", err)
		}
		dataDir = filepath.Join(configDir, "slacker")
	}

	sprinklerURL := os.Getenv("SPRINKLER_URL")
	if sprinklerURL == "" {
		sprinklerURL = "wss://" + client.DefaultServerAddress + "/ws"
	}

	slog.Info("loading configuration values")

	cfg := &config.ServerConfig{
		DataDir:              dataDir,
		SlackToken:           getSecretValue("SLACK_BOT_TOKEN"),
		SlackSigningSecret:   getSecretValue("SLACK_SIGNING_SECRET"),
		GitHubAppID:          os.Getenv("GITHUB_APP_ID"), // Not a secret, just config
		GitHubPrivateKey:     githubPrivateKey,
		GitHubInstallationID: os.Getenv("GITHUB_INSTALLATION_ID"), // Not a secret, just config
		SprinklerURL:         sprinklerURL,
	}

	slog.Info("configuration loaded",
		"has_slack_token", cfg.SlackToken != "",
		"has_slack_signing_secret", cfg.SlackSigningSecret != "",
		"has_github_app_id", cfg.GitHubAppID != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "",
		"has_github_installation_id", cfg.GitHubInstallationID != "")

	// Validate required fields
	if cfg.SlackToken == "" {
		return nil, errors.New("missing required configuration: SLACK_BOT_TOKEN (env var or secret)")
	}
	if cfg.SlackSigningSecret == "" {
		return nil, errors.New("missing required configuration: SLACK_SIGNING_SECRET (env var or secret)")
	}
	if cfg.GitHubAppID == "" {
		return nil, errors.New("missing required environment variable: GITHUB_APP_ID")
	}
	if cfg.GitHubPrivateKey == "" {
		return nil, errors.New("missing required configuration: GITHUB_PRIVATE_KEY (env var, file, or secret)")
	}
	if cfg.GitHubInstallationID == "" {
		return nil, errors.New("missing required environment variable: GITHUB_INSTALLATION_ID")
	}

	return cfg, nil
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("failed to write health response", "error", err)
	}
}

// securityHeadersMiddleware adds security headers to all responses.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security headers to prevent common attacks.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none';")
		w.Header().Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(w, r)
	})
}
