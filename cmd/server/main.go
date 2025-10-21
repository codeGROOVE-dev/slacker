// Package main implements the slacker server.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codeGROOVE-dev/gsm"
	"github.com/codeGROOVE-dev/slacker/internal/bot"
	"github.com/codeGROOVE-dev/slacker/internal/config"
	"github.com/codeGROOVE-dev/slacker/internal/github"
	"github.com/codeGROOVE-dev/slacker/internal/notify"
	"github.com/codeGROOVE-dev/slacker/internal/slack"
	"github.com/codeGROOVE-dev/slacker/internal/state"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
	"github.com/gorilla/mux"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// detectGCPProjectID attempts to detect the GCP project ID from the metadata service.
// Returns empty string if not running on GCP or detection fails.
func detectGCPProjectID(ctx context.Context) string {
	// Try metadata service (works on Cloud Run, GCE, GKE, Cloud Functions)
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://metadata.google.internal/computeMetadata/v1/project/project-id", http.NoBody)
	if err != nil {
		return ""
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := client.Do(req)
	if err != nil {
		slog.Debug("metadata service not available (not running on GCP?)", "error", err)
		return ""
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("failed to close metadata response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		slog.Debug("metadata service returned non-200", "status", resp.StatusCode)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Debug("failed to read metadata response", "error", err)
		return ""
	}

	projectID := strings.TrimSpace(string(body))
	if projectID == "" {
		return ""
	}

	return projectID
}

// Server configuration constants.
const (
	serverReadTimeout     = 15 * time.Second
	serverWriteTimeout    = 15 * time.Second
	oauthRateLimiterBurst = 20 // Maximum burst of OAuth requests allowed
)

func main() {
	// Configure logging with source locations and instance ID for better debugging
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Shorten source paths to relative paths for cleaner logs
			if a.Key == slog.SourceKey {
				if source, ok := a.Value.Any().(*slog.Source); ok {
					// Find project root by looking for /slacker/ in path
					if idx := strings.LastIndex(source.File, "/slacker/"); idx >= 0 {
						source.File = source.File[idx+9:] // Skip "/slacker/"
					}
				}
			}
			return a
		},
	})
	// Create logger with hostname as a default attribute
	// In Cloud Run, hostname uniquely identifies each instance (e.g., slacker-abc123-xyz789)
	// This is critical for disambiguating instances during rolling deployments
	logger := slog.New(logHandler).With("instance", hostname)
	slog.SetDefault(logger)

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
	configManager := config.New()

	// Initialize GitHub installation manager.
	githubManager, err := github.NewManager(ctx, cfg.GitHubAppID, cfg.GitHubPrivateKey, cfg.AllowPersonalAccounts)
	if err != nil {
		slog.Error("failed to initialize GitHub installation manager", "error", err)
		cancel() // Ensure cleanup happens before exit
		return 1
	}

	// Initialize Slack manager for multi-workspace support.
	// Tokens are fetched from GSM based on team_id from org configs.
	slackManager := slack.NewManager(cfg.SlackSigningSecret)

	// Initialize state store (in-memory + Datastore or JSON for persistence).
	var stateStore interface {
		Thread(owner, repo string, number int, channelID string) (state.ThreadInfo, bool)
		SaveThread(owner, repo string, number int, channelID string, info state.ThreadInfo) error
		LastDM(userID, prURL string) (time.Time, bool)
		RecordDM(userID, prURL string, sentAt time.Time) error
		DMMessage(userID, prURL string) (state.DMInfo, bool)
		SaveDMMessage(userID, prURL string, info state.DMInfo) error
		ListDMUsers(prURL string) []string
		LastDigest(userID, date string) (time.Time, bool)
		RecordDigest(userID, date string, sentAt time.Time) error
		WasProcessed(eventKey string) bool
		MarkProcessed(eventKey string, ttl time.Duration) error
		LastNotification(prURL string) time.Time
		RecordNotification(prURL string, notifiedAt time.Time) error
		Cleanup() error
		Close() error
	}

	// Check if Datastore should be used via DATASTORE=<database-id>
	// Examples:
	//   DATASTORE=slacker       -> Use Datastore with database ID "slacker"
	//   DATASTORE=(default)     -> Use default Datastore database
	//   DATASTORE=              -> JSON-only mode (no Datastore)
	//   (unset)                 -> JSON-only mode (no Datastore)
	datastoreDB := os.Getenv("DATASTORE")
	projectID := os.Getenv("GCP_PROJECT")

	// Auto-detect project ID from GCP metadata service if Datastore requested
	// This works when running on Cloud Run, GCE, GKE, etc.
	if datastoreDB != "" && projectID == "" {
		projectID = detectGCPProjectID(ctx)
		if projectID != "" {
			slog.Info("detected GCP project from metadata service",
				"project_id", projectID,
				"source", "metadata.google.internal")
		}
	}

	if datastoreDB != "" && projectID != "" {
		slog.Info("initializing Cloud Datastore for persistent state (with in-memory cache)",
			"project_id", projectID,
			"database", datastoreDB,
			"cache", "in-memory")
		var err error
		stateStore, err = state.NewDatastoreStore(ctx, projectID, datastoreDB)
		if err != nil {
			// FATAL: If DATASTORE is explicitly configured, fail startup on initialization errors.
			// This prevents silent fallback to memory-only mode which causes duplicate messages
			// during rolling deployments (no cross-instance event deduplication).
			slog.Error("FATAL: failed to initialize Cloud Datastore - DATASTORE variable is set but initialization failed",
				"project_id", projectID,
				"database", datastoreDB,
				"error", err,
				"note", "Set DATASTORE='' to use JSON files instead")
			cancel()
			return 1
		}
		slog.Info("successfully initialized Cloud Datastore with in-memory cache",
			"project_id", projectID,
			"database", datastoreDB,
			"mode", "hybrid: in-memory + Datastore")
	} else {
		var reason string
		if datastoreDB == "" {
			reason = "DATASTORE not set"
		} else {
			reason = "GCP_PROJECT not set and could not auto-detect"
		}
		slog.Info("using JSON files for persistent state (with in-memory cache)",
			"path", "os.UserCacheDir()/slacker/state",
			"reason", reason,
			"mode", "hybrid: in-memory + JSON files")
		var err error
		stateStore, err = state.NewJSONStore()
		if err != nil {
			slog.Error("failed to initialize JSON store", "error", err)
			cancel()
			return 1
		}
	}

	// Ensure state store is closed on exit
	defer func() {
		if err := stateStore.Close(); err != nil {
			slog.Warn("failed to close state store", "error", err)
		}
	}()

	// Set state store on Slack manager for DM message tracking
	slackManager.SetStateStore(stateStore)
	slog.Info("configured Slack manager with state store for DM tracking")

	// Initialize notification manager for multi-workspace notifications.
	notifier := notify.New(slackManager, configManager)

	// Initialize event router for multi-workspace event handling.
	eventRouter := slack.NewEventRouter(slackManager)

	// Initialize home view handler
	homeHandler := slack.NewHomeHandler(slackManager, githubManager, configManager, stateStore)
	slackManager.SetHomeViewHandler(homeHandler.HandleAppHomeOpened)

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
	router.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods("GET")

	// Health endpoints
	router.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			slog.Error("failed to write health response", "error", err)
		}
	}).Methods("GET")
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
	router.HandleFunc("/slack/interactive-endpoint", eventRouter.HandleInteractions).Methods("POST")
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
		if err := runBotCoordinators(ctx, slackManager, githubManager, configManager, notifier, stateStore, cfg.SprinklerURL); err != nil {
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
	stateStore      state.Store
	active          map[string]context.CancelFunc
	failed          map[string]time.Time
	lastHealthCheck time.Time
	sprinklerURL    string
	mu              sync.Mutex
}

// handleCoordinatorExit cleans up after a coordinator exits.
func (cm *coordinatorManager) handleCoordinatorExit(org, sprinklerURL string, err error) {
	// Acquire lock once for all state modifications to prevent race conditions
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err != nil {
		if errors.Is(err, context.Canceled) {
			slog.Info("coordinator stopped due to context cancellation", "org", org)
		} else {
			slog.Error("coordinator exited unexpectedly - THIS SHOULD NOT HAPPEN",
				"org", org,
				"error", err,
				"error_type", fmt.Sprintf("%T", err))
			cm.failed[org] = time.Now()
		}
	} else {
		slog.Error("coordinator exited with nil error - THIS SHOULD NOT HAPPEN",
			"org", org,
			"sprinkler_url", sprinklerURL)
		cm.failed[org] = time.Now()
	}

	delete(cm.active, org)
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
		cm.stateStore,
	)

	// Run startup reconciliation to catch up on missed notifications
	go func() {
		coordinator.StartupReconciliation(orgCtx)
	}()

	go func(org, teamID string, coord *bot.Coordinator, orgCtx context.Context) {
		slog.Info("starting coordinator for org",
			"org", org,
			"team_id", teamID,
			"sprinkler_url", cm.sprinklerURL)

		// Start polling goroutine for this coordinator
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()

			for {
				select {
				case <-orgCtx.Done():
					slog.Info("stopping polling for org", "org", org)
					return
				case <-ticker.C:
					coord.PollAndReconcile(orgCtx)
				}
			}
		}()

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
	stateStore interface {
		Thread(owner, repo string, number int, channelID string) (state.ThreadInfo, bool)
		SaveThread(owner, repo string, number int, channelID string, info state.ThreadInfo) error
		LastDM(userID, prURL string) (time.Time, bool)
		RecordDM(userID, prURL string, sentAt time.Time) error
		DMMessage(userID, prURL string) (state.DMInfo, bool)
		SaveDMMessage(userID, prURL string, info state.DMInfo) error
		ListDMUsers(prURL string) []string
		LastDigest(userID, date string) (time.Time, bool)
		RecordDigest(userID, date string, sentAt time.Time) error
		WasProcessed(eventKey string) bool
		MarkProcessed(eventKey string, ttl time.Duration) error
		LastNotification(prURL string) time.Time
		RecordNotification(prURL string, notifiedAt time.Time) error
		Cleanup() error
		Close() error
	},
	sprinklerURL string,
) error {
	cm := &coordinatorManager{
		active:          make(map[string]context.CancelFunc),
		failed:          make(map[string]time.Time),
		slackManager:    slackManager,
		githubManager:   githubManager,
		configManager:   configManager,
		notifier:        notifier,
		stateStore:      stateStore,
		sprinklerURL:    sprinklerURL,
		lastHealthCheck: time.Now(),
	}

	// Initialize daily digest scheduler
	dailyDigest := notify.NewDailyDigestScheduler(notifier, githubManager, configManager, stateStore)

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

	// Poll for PRs every 5 minutes (safety net for missed sprinkler events)
	pollTicker := time.NewTicker(5 * time.Minute)
	defer pollTicker.Stop()

	// Check for daily digest candidates every hour
	dailyDigestTicker := time.NewTicker(1 * time.Hour)
	defer dailyDigestTicker.Stop()

	// Run daily digest check immediately on startup
	// (in case server starts during someone's 8-9am window)
	go func() {
		dailyDigest.CheckAndSend(ctx)
	}()

	// Setup state cleanup ticker (hourly)
	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

	// Run cleanup once on startup
	go func() {
		if err := stateStore.Cleanup(); err != nil {
			slog.Warn("initial state cleanup failed", "error", err)
		}
	}()

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

		case <-pollTicker.C:
			// Poll all active coordinators
			cm.handlePolling(ctx)

		case <-dailyDigestTicker.C:
			// Check for daily digest candidates across all orgs
			go func() {
				dailyDigest.CheckAndSend(ctx)
			}()

		case <-cleanupTicker.C:
			// Periodic cleanup of old state data
			go func() {
				if err := stateStore.Cleanup(); err != nil {
					slog.Warn("state cleanup failed", "error", err)
				} else {
					slog.Debug("state cleanup completed successfully")
				}
			}()
		}
	}
}

// handlePolling triggers polling for all active coordinators.
func (cm *coordinatorManager) handlePolling(_ context.Context) {
	cm.mu.Lock()
	activeCount := len(cm.active)
	cm.mu.Unlock()

	if activeCount == 0 {
		slog.Debug("no active coordinators to poll")
		return
	}

	slog.Debug("triggering PR polling for all coordinators",
		"active_count", activeCount)

	// Polling is handled per-coordinator in their own goroutines
	// We rely on each coordinator to implement pollAndReconcile
	// For now, this is a placeholder - actual implementation would need
	// access to coordinators or a different architecture
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

	// Parse personal accounts flag (default: false for DoS protection)
	allowPersonalAccounts := os.Getenv("ALLOW_PERSONAL_ACCOUNTS") == "true"

	cfg := &config.ServerConfig{
		DataDir:               dataDir,
		SlackSigningSecret:    getSecretValue("SLACK_SIGNING_SECRET"),
		GitHubAppID:           os.Getenv("GITHUB_APP_ID"), // Not a secret, just config
		GitHubPrivateKey:      githubPrivateKey,
		SprinklerURL:          sprinklerURL,
		AllowPersonalAccounts: allowPersonalAccounts,
	}

	slog.Info("configuration loaded",
		"has_slack_signing_secret", cfg.SlackSigningSecret != "",
		"has_github_app_id", cfg.GitHubAppID != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "",
		"allow_personal_accounts", cfg.AllowPersonalAccounts)

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
