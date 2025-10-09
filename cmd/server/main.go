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
	// Configure logging with source locations for better debugging
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
	})
	slog.SetDefault(slog.New(logHandler))

	// Load configuration from environment.
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Run the server and handle exit code
	exitCode := run(ctx, cancel, cfg)
	cancel() // Cancel context before exit
	os.Exit(exitCode)
}

func run(ctx context.Context, cancel context.CancelFunc, cfg *config.ServerConfig) int {
	// Handle graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		slog.Info("received shutdown signal, starting graceful stop",
			"signal", sig.String(),
			"signal_number", sig)
		cancel()
	}()

	// Log configuration without secrets.
	slog.Info("configuration loaded",
		"data_dir", cfg.DataDir,
		"sprinkler_url", cfg.SprinklerURL,
		"github_app_id", cfg.GitHubAppID,
		"has_slack_signing_secret", cfg.SlackSigningSecret != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "",
		"startup_message", "Starting Slacker server...")

	// Initialize config manager for repo configs.
	configManager := config.New(ctx)

	// Initialize GitHub installation manager.
	githubManager, err := github.NewManager(ctx, cfg.GitHubAppID, cfg.GitHubPrivateKey)
	if err != nil {
		slog.Error("failed to initialize GitHub installation manager", "error", err)
		cancel() // Ensure cleanup happens before exit
		return 1
	}

	// Initialize Slack manager for multi-workspace support.
	// Tokens are fetched from GSM based on team_id from org configs.
	slackManager := slack.NewManager(cfg.SlackSigningSecret)

	// Initialize notification manager for multi-workspace notifications.
	notifier := notify.New(slackManager, configManager)

	// Initialize event router for multi-workspace event handling.
	eventRouter := slack.NewEventRouter(slackManager)

	// Setup HTTP routes with security middleware.
	router := mux.NewRouter()
	router.Use(securityHeadersMiddleware)

	// Root endpoint - blank
	router.HandleFunc("/", blankHandler).Methods("GET")

	// Health endpoints
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/healthz", makeHealthzHandler(githubManager)).Methods("GET")

	// Slack endpoints - routed to workspace-specific clients
	router.HandleFunc("/slack/events", eventRouter.HandleEvents).Methods("POST")
	router.HandleFunc("/slack/interactions", eventRouter.HandleInteractions).Methods("POST")
	router.HandleFunc("/slack/slash", eventRouter.HandleSlashCommand).Methods("POST")

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
	// This runs indefinitely and handles its own retries - should never return an error
	// unless the context is cancelled (clean shutdown).
	eg.Go(func() error {
		if err := runBotCoordinators(ctx, slackManager, githubManager, configManager, notifier, cfg.SprinklerURL); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			// Log unexpected error but don't propagate to errgroup
			// (would trigger shutdown of entire server)
			slog.Error("bot coordinators stopped unexpectedly", "error", err)
		}
		return nil
	})

	// Start notification scheduler.
	eg.Go(func() error {
		slog.Debug("starting notifier goroutine")
		err := notifier.Run(ctx)
		slog.Debug("notifier goroutine ended", "error", err)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})

	// Wait for all services.
	slog.Debug("waiting for all services to complete")
	if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("server error", "error", err)
		return 1
	}

	slog.Info("server stopped")
	return 0
}

// runBotCoordinators manages bot coordinators for all GitHub installations.
// It spawns one coordinator per org and refreshes the list every 5 minutes.
// Failed coordinators are automatically restarted every minute.
func runBotCoordinators(
	ctx context.Context,
	slackManager *slack.Manager,
	githubManager *github.Manager,
	configManager *config.Manager,
	notifier *notify.Manager,
	sprinklerURL string,
) error {
	activeCoordinators := make(map[string]context.CancelFunc)
	failedCoordinators := make(map[string]time.Time) // Track when coordinators failed
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
				// Mark as failed so we retry later (already holding mu)
				failedCoordinators[org] = time.Now()
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
			if !exists || cfg.Global.TeamID == "" {
				slog.Debug("skipping org without Slack configuration", "org", org)
				continue
			}

			// Get team_id from config
			teamID := cfg.Global.TeamID

			// Get Slack client for this workspace
			slackClient, err := slackManager.Client(ctx, teamID)
			if err != nil {
				slog.Error("failed to get Slack client for workspace",
					"org", org,
					"team_id", teamID,
					"error", err)
				// Mark as failed so we retry later (already holding mu)
				failedCoordinators[org] = time.Now()
				continue
			}

			// Start coordinator in goroutine with org-specific context
			orgCtx, cancel := context.WithCancel(ctx)
			activeCoordinators[org] = cancel

			// Clear from failed list since we're starting it
			delete(failedCoordinators, org)

			// Create coordinator for this org with org-specific context
			coordinator := bot.New(
				orgCtx,
				slackClient,
				githubClient,
				configManager,
				notifier,
				sprinklerURL,
			)

			go func(org, teamID string, coord *bot.Coordinator, orgCtx context.Context) {
				slog.Info("starting coordinator for org",
					"org", org,
					"team_id", teamID,
					"sprinkler_url", sprinklerURL)

				err := coord.RunWithSprinklerClient(orgCtx)

				// Coordinator should NEVER exit unless context is cancelled
				if err != nil {
					if errors.Is(err, context.Canceled) {
						slog.Info("coordinator stopped due to context cancellation", "org", org)
					} else {
						slog.Error("coordinator exited unexpectedly - THIS SHOULD NOT HAPPEN",
							"org", org,
							"error", err,
							"error_type", fmt.Sprintf("%T", err))
						// Mark as failed so we retry
						mu.Lock()
						failedCoordinators[org] = time.Now()
						mu.Unlock()
					}
				} else {
					// This should NEVER happen - RunWithSprinklerClient has infinite retry loop
					slog.Error("coordinator exited with nil error - THIS SHOULD NOT HAPPEN",
						"org", org,
						"sprinkler_url", sprinklerURL)
					mu.Lock()
					failedCoordinators[org] = time.Now()
					mu.Unlock()
				}

				// Clean up when coordinator exits
				mu.Lock()
				delete(activeCoordinators, org)
				mu.Unlock()
			}(org, teamID, coordinator, orgCtx)
		}
	}

	// Start initial coordinators
	startCoordinators()

	// Refresh installations every 5 minutes
	installationTicker := time.NewTicker(5 * time.Minute)
	defer installationTicker.Stop()

	// Retry failed coordinators every minute
	retryTicker := time.NewTicker(1 * time.Minute)
	defer retryTicker.Stop()

	// Health check: fail if no coordinators are active for too long
	healthCheckTicker := time.NewTicker(15 * time.Second)
	defer healthCheckTicker.Stop()
	var lastHealthCheck time.Time
	const maxDowntime = 1 * time.Minute

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

		case <-healthCheckTicker.C:
			mu.Lock()
			activeCount := len(activeCoordinators)
			failedCount := len(failedCoordinators)
			totalOrgs := len(githubManager.AllOrgs())
			mu.Unlock()

			if activeCount == 0 && totalOrgs > 0 {
				// No active coordinators but we have orgs - this is a problem
				if !lastHealthCheck.IsZero() && time.Since(lastHealthCheck) > maxDowntime {
					slog.Error("FATAL: no active coordinators for too long",
						"total_orgs", totalOrgs,
						"failed_coordinators", failedCount,
						"last_active", lastHealthCheck,
						"downtime", time.Since(lastHealthCheck))
					return errors.New("no active coordinators for extended period")
				}
				slog.Warn("no active coordinators - will fail soon",
					"total_orgs", totalOrgs,
					"failed_coordinators", failedCount,
					"time_until_failure", maxDowntime-time.Since(lastHealthCheck))
			} else if activeCount > 0 {
				lastHealthCheck = time.Now()
				slog.Debug("coordinator health check passed",
					"active_coordinators", activeCount,
					"failed_coordinators", failedCount,
					"total_orgs", totalOrgs)
			}

		case <-retryTicker.C:
			mu.Lock()
			failedCount := len(failedCoordinators)
			mu.Unlock()

			if failedCount > 0 {
				slog.Info("retrying failed coordinators",
					"failed_count", failedCount)

				// Refresh installations to get latest GitHub clients
				if err := githubManager.RefreshInstallations(ctx); err != nil {
					if !errors.Is(err, context.Canceled) {
						slog.Error("failed to refresh installations during retry", "error", err)
					}
					continue
				}

				// Try to start failed coordinators
				startCoordinators()

				mu.Lock()
				activeCount := len(activeCoordinators)
				mu.Unlock()

				slog.Info("retry attempt complete",
					"active_coordinators", activeCount)
			}

		case <-installationTicker.C:
			mu.Lock()
			activeCount := len(activeCoordinators)
			mu.Unlock()

			slog.Info("refreshing GitHub installations",
				"active_coordinators", activeCount)

			if err := githubManager.RefreshInstallations(ctx); err != nil {
				if !errors.Is(err, context.Canceled) {
					slog.Error("failed to refresh installations", "error", err)
				}
				continue
			}

			startCoordinators()

			mu.Lock()
			newActiveCount := len(activeCoordinators)
			mu.Unlock()

			slog.Info("refresh complete",
				"active_coordinators", newActiveCount)
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
		value, err := gsm.Fetch(ctx, name)
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
		SlackSigningSecret: getSecretValue("SLACK_SIGNING_SECRET"),
		GitHubAppID:        os.Getenv("GITHUB_APP_ID"), // Not a secret, just config
		GitHubPrivateKey:   githubPrivateKey,
		SprinklerURL:       sprinklerURL,
	}

	slog.Info("configuration loaded",
		"has_slack_signing_secret", cfg.SlackSigningSecret != "",
		"has_github_app_id", cfg.GitHubAppID != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "")

	// Validate required fields
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

func blankHandler(w http.ResponseWriter, _ *http.Request) {
	// Blank homepage - no content
	w.WriteHeader(http.StatusOK)
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	// Basic health check - just confirms the HTTP server is responding
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("failed to write health response", "error", err)
	}
}

// makeHealthzHandler creates a more detailed health check that verifies coordinators are running.
// This is useful for Cloud Run liveness checks.
func makeHealthzHandler(githubManager *github.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		orgs := githubManager.AllOrgs()

		// If we have GitHub installations configured, we should have coordinators
		if len(orgs) == 0 {
			// No orgs configured yet - this is OK during startup
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("OK - no orgs configured")); err != nil {
				slog.Error("failed to write healthz response", "error", err)
			}
			return
		}

		// We have orgs - assume coordinators should be running
		// (This is a basic check - the coordinator health check ticker provides more detailed monitoring)
		w.WriteHeader(http.StatusOK)
		response := fmt.Sprintf("OK - %d orgs configured", len(orgs))
		if _, err := w.Write([]byte(response)); err != nil {
			slog.Error("failed to write healthz response", "error", err)
		}
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
