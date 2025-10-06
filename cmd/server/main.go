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
	"sync"
	"syscall"
	"time"

	"github.com/codeGROOVE-dev/gsm"
	"github.com/codeGROOVE-dev/slacker/pkg/bot"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
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
		"has_slack_token", cfg.SlackToken != "",
		"has_slack_signing_secret", cfg.SlackSigningSecret != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "",
		"startup_message", "Starting Slacker server...")

	// Initialize state manager with file persistence.
	stateManager := state.New(cfg.DataDir)

	// Initialize config manager for repo configs.
	configManager := config.New(ctx)

	// Initialize GitHub installation manager.
	githubManager, err := github.NewManager(ctx, cfg.GitHubAppID, cfg.GitHubPrivateKey)
	if err != nil {
		slog.Error("failed to initialize GitHub installation manager", "error", err)
		cancel() // Ensure cleanup happens before exit
		return   // Let main return naturally instead of os.Exit
	}

	// Initialize Slack client.
	slackClient := slack.New(cfg.SlackToken, cfg.SlackSigningSecret)

	// Initialize notification manager.
	notifier := notify.New(slackClient, stateManager, configManager)

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

	// Start bot coordinators for each GitHub installation.
	eg.Go(func() error {
		return runBotCoordinators(ctx, slackClient, githubManager, stateManager, configManager, notifier, cfg.SprinklerURL)
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

// runBotCoordinators manages bot coordinators for all GitHub installations.
// It spawns one coordinator per org and refreshes the list every 15 minutes.
func runBotCoordinators(
	ctx context.Context,
	slackClient *slack.Client,
	githubManager *github.Manager,
	stateManager *state.Manager,
	configManager *config.Manager,
	notifier *notify.Manager,
	sprinklerURL string,
) error {
	activeCoordinators := make(map[string]context.CancelFunc)
	var mu sync.Mutex

	// startCoordinators creates coordinators for all orgs that don't already have one,
	// and stops coordinators for orgs that no longer exist.
	startCoordinators := func() {
		mu.Lock()
		defer mu.Unlock()

		orgs := githubManager.AllOrgs()
		slog.Info("checking GitHub installations", "total_orgs", len(orgs))

		// Create map of current orgs for quick lookup
		currentOrgs := make(map[string]bool)
		for _, org := range orgs {
			currentOrgs[org] = true
		}

		// Stop coordinators for orgs that no longer exist
		for org, cancel := range activeCoordinators {
			if !currentOrgs[org] {
				slog.Info("stopping coordinator for removed org", "org", org)
				cancel()
				delete(activeCoordinators, org)
			}
		}

		// Start coordinators for new orgs
		for _, org := range orgs {
			// Skip if already running
			if _, exists := activeCoordinators[org]; exists {
				continue
			}

			// Get GitHub client for this org
			githubClient, exists := githubManager.ClientForOrg(org)
			if !exists {
				slog.Warn("no GitHub client for org", "org", org)
				continue
			}

			// Set GitHub client in config manager for this org
			configManager.SetGitHubClient(org, githubClient.Client())

			// Load config to check if Slack is configured
			if err := configManager.LoadConfig(ctx, org); err != nil {
				slog.Warn("failed to load config for org", "org", org, "error", err)
				continue
			}

			cfg, exists := configManager.Config(org)
			if !exists || cfg.Global.Slack == "" {
				slog.Debug("skipping org without Slack configuration", "org", org)
				continue
			}

			// Create coordinator for this org
			coordinator := bot.New(
				ctx,
				slackClient,
				githubClient,
				stateManager,
				configManager,
				notifier,
				sprinklerURL,
			)

			// Start coordinator in goroutine
			orgCtx, cancel := context.WithCancel(ctx)
			activeCoordinators[org] = cancel

			go func(org string, coord *bot.Coordinator) {
				slog.Info("starting coordinator for org",
					"org", org,
					"workspace", cfg.Global.Slack,
					"sprinkler_url", sprinklerURL)
				if err := coord.RunWithSprinklerClient(orgCtx); err != nil && !errors.Is(err, context.Canceled) {
					slog.Error("coordinator error", "org", org, "error", err)
				}
				slog.Info("coordinator stopped", "org", org)

				// Clean up when coordinator exits
				mu.Lock()
				delete(activeCoordinators, org)
				mu.Unlock()
			}(org, coordinator)
		}
	}

	// Start initial coordinators
	startCoordinators()

	// Refresh installations every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("stopping all coordinators")
			mu.Lock()
			for org, cancel := range activeCoordinators {
				slog.Info("stopping coordinator", "org", org)
				cancel()
			}
			mu.Unlock()
			return ctx.Err()
		case <-ticker.C:
			slog.Info("refreshing GitHub installations",
				"active_coordinators", len(activeCoordinators))
			if err := githubManager.RefreshInstallations(ctx); err != nil {
				slog.Error("failed to refresh installations", "error", err)
				continue
			}
			startCoordinators()
			slog.Info("refresh complete",
				"active_coordinators", len(activeCoordinators))
		}
	}
}

func loadConfig() (*config.ServerConfig, error) {
	ctx := context.Background()

	// Helper function to get secret values
	// Environment variables take precedence, then Secret Manager
	getSecretValue := func(name string) string {
		// Environment variable takes precedence
		if value := os.Getenv(name); value != "" {
			slog.Info("using environment variable",
				"name", name,
				"source", "environment")
			return value
		}

		// Try Secret Manager using gsm library
		slog.Info("attempting to fetch secret from Secret Manager",
			"name", name)
		value, err := gsm.Secret(ctx, name)
		if err != nil {
			slog.Error("failed to fetch secret from Secret Manager",
				"name", name,
				"error", err)
			return ""
		}
		slog.Info("successfully fetched secret from Secret Manager",
			"name", name,
			"has_value", value != "")
		return value
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
		DataDir:            dataDir,
		SlackToken:         getSecretValue("SLACK_BOT_TOKEN"),
		SlackSigningSecret: getSecretValue("SLACK_SIGNING_SECRET"),
		GitHubAppID:        os.Getenv("GITHUB_APP_ID"), // Not a secret, just config
		GitHubPrivateKey:   githubPrivateKey,
		SprinklerURL:       sprinklerURL,
	}

	slog.Info("configuration loaded",
		"has_slack_token", cfg.SlackToken != "",
		"has_slack_signing_secret", cfg.SlackSigningSecret != "",
		"has_github_app_id", cfg.GitHubAppID != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "")

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
