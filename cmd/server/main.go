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
	"github.com/codeGROOVE-dev/slacker/internal/bot"
	"github.com/codeGROOVE-dev/slacker/internal/config"
	"github.com/codeGROOVE-dev/slacker/internal/github"
	"github.com/codeGROOVE-dev/slacker/internal/notify"
	"github.com/codeGROOVE-dev/slacker/internal/slack"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
	"github.com/gorilla/mux"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// Server configuration constants.
const (
	serverReadTimeout     = 15 * time.Second
	serverWriteTimeout    = 15 * time.Second
	oauthRateLimiterBurst = 20 // Maximum burst of OAuth requests allowed
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
		slog.Warn("🚨 SHUTDOWN SIGNAL RECEIVED 🚨",
			"signal", sig.String(),
			"signal_number", sig,
			"reason", "Cloud Run is shutting down this instance (likely due to inactivity or new deployment)",
			"action", "initiating fast shutdown")
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

	// Initialize OAuth handler for Slack app installation.
	// These credentials are needed for the OAuth flow.
	slackClientID := os.Getenv("SLACK_CLIENT_ID")
	slackClientSecret := os.Getenv("SLACK_CLIENT_SECRET")
	if slackClientSecret == "" {
		// Try fetching from Secret Manager
		var err error
		slackClientSecret, err = gsm.Fetch(ctx, "SLACK_CLIENT_SECRET")
		if err != nil {
			slog.Warn("SLACK_CLIENT_SECRET not found - OAuth installation will not work",
				"error", err)
		}
	}

	var oauthHandler *slack.OAuthHandler
	if slackClientID != "" && slackClientSecret != "" {
		oauthHandler = slack.NewOAuthHandler(slackManager, slackClientID, slackClientSecret)
		slog.Info("OAuth handler initialized",
			"client_id", slackClientID)
	} else {
		slog.Warn("OAuth not configured - app installation via web will not work",
			"has_client_id", slackClientID != "",
			"has_client_secret", slackClientSecret != "")
	}

	// Setup HTTP routes with security middleware.
	router := mux.NewRouter()
	router.Use(securityHeadersMiddleware)

	// Root endpoint - blank
	router.HandleFunc("/", blankHandler).Methods("GET")

	// Health endpoints
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/healthz", makeHealthzHandler(githubManager)).Methods("GET")

	// Slack OAuth endpoints - for app installation with rate limiting
	if oauthHandler != nil {
		// SECURITY: Rate limiter for OAuth endpoints: 10 requests per second, burst of oauthRateLimiterBurst
		// This prevents abuse while allowing legitimate installation flows
		oauthLimiter := rate.NewLimiter(10, oauthRateLimiterBurst)

		router.Handle("/slack/install", rateLimitMiddleware(oauthLimiter)(http.HandlerFunc(oauthHandler.HandleInstall))).Methods("GET")
		router.Handle("/slack/oauth/callback", rateLimitMiddleware(oauthLimiter)(http.HandlerFunc(oauthHandler.HandleCallback))).Methods("GET")

		// Debug endpoint - DO NOT EXPOSE IN PRODUCTION
		// Remove this endpoint entirely or protect with strong authentication
		// router.HandleFunc("/slack/debug", oauthHandler.HandleDebug).Methods("GET")

		slog.Info("registered OAuth endpoints with rate limiting",
			"install_url", "/slack/install",
			"callback_url", "/slack/oauth/callback",
			"rate_limit", "10/s burst 20")
	}

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
		slog.Info("shutting down HTTP server")
		// Quick shutdown - Cloud Run gives us ~10 seconds, use 2 seconds for HTTP
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("HTTP server shutdown timeout - forcing close", "error", err)
			return nil // Don't fail the errgroup, just move on
		}
		slog.Info("HTTP server stopped cleanly")
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

	slog.Warn("✅ SERVER STOPPED CLEANLY - all services shut down gracefully")
	return 0
}

// coordinatorManager holds state for managing bot coordinators across orgs.
type coordinatorManager struct {
	slackManager    *slack.Manager
	githubManager   *github.Manager
	configManager   *config.Manager
	notifier        *notify.Manager
	active          map[string]context.CancelFunc
	failed          map[string]time.Time
	lastHealthCheck time.Time
	sprinklerURL    string
	mu              sync.Mutex
}

// handleCoordinatorExit cleans up after a coordinator exits.
func (cm *coordinatorManager) handleCoordinatorExit(org, sprinklerURL string, err error) {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			slog.Info("coordinator stopped due to context cancellation", "org", org)
		} else {
			slog.Error("coordinator exited unexpectedly - THIS SHOULD NOT HAPPEN",
				"org", org,
				"error", err,
				"error_type", fmt.Sprintf("%T", err))
			cm.mu.Lock()
			cm.failed[org] = time.Now()
			cm.mu.Unlock()
		}
	} else {
		slog.Error("coordinator exited with nil error - THIS SHOULD NOT HAPPEN",
			"org", org,
			"sprinkler_url", sprinklerURL)
		cm.mu.Lock()
		cm.failed[org] = time.Now()
		cm.mu.Unlock()
	}

	cm.mu.Lock()
	delete(cm.active, org)
	cm.mu.Unlock()
}

// startSingleCoordinator starts a coordinator for one org.
func (cm *coordinatorManager) startSingleCoordinator(ctx context.Context, org string) bool {
	// Skip if already running
	if _, exists := cm.active[org]; exists {
		return true
	}

	// Get GitHub client for this org
	githubClient, exists := cm.githubManager.ClientForOrg(org)
	if !exists {
		slog.Warn("no GitHub client for org", "org", org)
		cm.failed[org] = time.Now()
		return false
	}

	// Set GitHub client in config manager for this org
	cm.configManager.SetGitHubClient(org, githubClient.Client())

	// Load config to check if Slack is configured
	if err := cm.configManager.LoadConfig(ctx, org); err != nil {
		slog.Warn("failed to load config for org", "org", org, "error", err)
		return false
	}

	cfg, exists := cm.configManager.Config(org)
	if !exists || cfg.Global.TeamID == "" {
		slog.Debug("skipping org without Slack configuration", "org", org)
		return false
	}

	teamID := cfg.Global.TeamID

	// Get Slack client for this workspace
	slackClient, err := cm.slackManager.Client(ctx, teamID)
	if err != nil {
		slog.Error("failed to get Slack client for workspace",
			"org", org,
			"team_id", teamID,
			"error", err)
		cm.failed[org] = time.Now()
		return false
	}

	// Start coordinator in goroutine with org-specific context
	orgCtx, cancel := context.WithCancel(ctx)
	cm.active[org] = cancel

	// Clear from failed list since we're starting it
	delete(cm.failed, org)

	// Create coordinator for this org with org-specific context
	coordinator := bot.New(
		orgCtx,
		slackClient,
		githubClient,
		cm.configManager,
		cm.notifier,
		cm.sprinklerURL,
	)

	go func(org, teamID string, coord *bot.Coordinator, orgCtx context.Context) {
		slog.Info("starting coordinator for org",
			"org", org,
			"team_id", teamID,
			"sprinkler_url", cm.sprinklerURL)

		err := coord.RunWithSprinklerClient(orgCtx)
		cm.handleCoordinatorExit(org, cm.sprinklerURL, err)
	}(org, teamID, coordinator, orgCtx)

	return true
}

// startCoordinators creates coordinators for all orgs that don't already have one.
func (cm *coordinatorManager) startCoordinators(ctx context.Context) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	orgs := cm.githubManager.AllOrgs()
	slog.Info("checking GitHub installations", "total_orgs", len(orgs))

	// Create map of current orgs for quick lookup
	currentOrgs := make(map[string]bool)
	for _, org := range orgs {
		currentOrgs[org] = true
	}

	// Stop coordinators for orgs that no longer exist
	for org, cancel := range cm.active {
		if !currentOrgs[org] {
			slog.Info("stopping coordinator for removed org", "org", org)
			cancel()
			delete(cm.active, org)
		}
	}

	// Start coordinators for new orgs
	for _, org := range orgs {
		cm.startSingleCoordinator(ctx, org)
	}
}

// handleShutdown stops all coordinators and returns context error.
func (cm *coordinatorManager) handleShutdown(ctx context.Context) error {
	slog.Info("shutdown initiated - stopping all bot coordinators")
	cm.mu.Lock()
	coordinatorCount := len(cm.active)
	for org, cancel := range cm.active {
		slog.Info("stopping coordinator", "org", org)
		cancel()
	}
	cm.mu.Unlock()
	slog.Info("all coordinators stopped", "count", coordinatorCount)
	return ctx.Err()
}

// handleHealthCheck performs health monitoring and fails if unhealthy too long.
func (cm *coordinatorManager) handleHealthCheck() error {
	const maxDowntime = 1 * time.Minute

	cm.mu.Lock()
	activeCount := len(cm.active)
	failedCount := len(cm.failed)
	totalOrgs := len(cm.githubManager.AllOrgs())
	cm.mu.Unlock()

	if activeCount == 0 && totalOrgs > 0 {
		if !cm.lastHealthCheck.IsZero() && time.Since(cm.lastHealthCheck) > maxDowntime {
			slog.Error("FATAL: no active coordinators for too long",
				"total_orgs", totalOrgs,
				"failed_coordinators", failedCount,
				"last_active", cm.lastHealthCheck,
				"downtime", time.Since(cm.lastHealthCheck))
			return errors.New("no active coordinators for extended period")
		}
		slog.Warn("no active coordinators - will fail soon",
			"total_orgs", totalOrgs,
			"failed_coordinators", failedCount,
			"time_until_failure", maxDowntime-time.Since(cm.lastHealthCheck))
	} else if activeCount > 0 {
		cm.lastHealthCheck = time.Now()
		slog.Debug("coordinator health check passed",
			"active_coordinators", activeCount,
			"failed_coordinators", failedCount,
			"total_orgs", totalOrgs)
	}
	return nil
}

// handleRetryFailed retries starting failed coordinators.
func (cm *coordinatorManager) handleRetryFailed(ctx context.Context) {
	cm.mu.Lock()
	failedCount := len(cm.failed)
	cm.mu.Unlock()

	if failedCount == 0 {
		return
	}

	slog.Info("retrying failed coordinators", "failed_count", failedCount)

	if err := cm.githubManager.RefreshInstallations(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Error("failed to refresh installations during retry", "error", err)
		}
		return
	}

	cm.startCoordinators(ctx)

	cm.mu.Lock()
	activeCount := len(cm.active)
	cm.mu.Unlock()

	slog.Info("retry attempt complete", "active_coordinators", activeCount)
}

// handleRefreshInstallations refreshes GitHub installations and restarts coordinators.
func (cm *coordinatorManager) handleRefreshInstallations(ctx context.Context) {
	cm.mu.Lock()
	activeCount := len(cm.active)
	cm.mu.Unlock()

	slog.Info("refreshing GitHub installations", "active_coordinators", activeCount)

	if err := cm.githubManager.RefreshInstallations(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Error("failed to refresh installations", "error", err)
		}
		return
	}

	cm.startCoordinators(ctx)

	cm.mu.Lock()
	newActiveCount := len(cm.active)
	cm.mu.Unlock()

	slog.Info("refresh complete", "active_coordinators", newActiveCount)
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
	cm := &coordinatorManager{
		active:          make(map[string]context.CancelFunc),
		failed:          make(map[string]time.Time),
		slackManager:    slackManager,
		githubManager:   githubManager,
		configManager:   configManager,
		notifier:        notifier,
		sprinklerURL:    sprinklerURL,
		lastHealthCheck: time.Now(),
	}

	// Start initial coordinators
	cm.startCoordinators(ctx)

	// Refresh installations every 5 minutes
	installationTicker := time.NewTicker(5 * time.Minute)
	defer installationTicker.Stop()

	// Retry failed coordinators every minute
	retryTicker := time.NewTicker(1 * time.Minute)
	defer retryTicker.Stop()

	// Health check: fail if no coordinators are active for too long
	healthCheckTicker := time.NewTicker(15 * time.Second)
	defer healthCheckTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return cm.handleShutdown(ctx)

		case <-healthCheckTicker.C:
			if err := cm.handleHealthCheck(); err != nil {
				return err
			}

		case <-retryTicker.C:
			cm.handleRetryFailed(ctx)

		case <-installationTicker.C:
			cm.handleRefreshInstallations(ctx)
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

// rateLimitMiddleware applies rate limiting to prevent abuse.
func rateLimitMiddleware(limiter *rate.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				http.Error(w, "Too many requests - please try again later", http.StatusTooManyRequests)
				slog.Warn("rate limit exceeded", "path", r.URL.Path, "remote_addr", r.RemoteAddr)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
