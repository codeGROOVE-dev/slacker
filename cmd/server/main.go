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
	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/gorilla/mux"
	"golang.org/x/sync/errgroup"
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
		os.Exit(1)
	}

	// Initialize Slack client.
	slackClient := slack.New(cfg.SlackToken, cfg.SlackSigningSecret)

	// Initialize notification manager.
	notifier := notify.New(slackClient, stateManager)

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
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
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
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown failed: %w", err)
		}
		return nil
	})

	// Start bot coordinator.
	eg.Go(func() error {
		return botCoordinator.Run(ctx)
	})

	// Start notification scheduler.
	eg.Go(func() error {
		return notifier.Run(ctx)
	})

	// Wait for all services.
	if err := eg.Wait(); err != nil {
		slog.Error("server error", "error", err)
	}
	slog.Info("server stopped")
}

func loadConfig() (*config.ServerConfig, error) {
	// Load GitHub private key from environment or file.
	githubPrivateKey := os.Getenv("GITHUB_PRIVATE_KEY")
	if githubPrivateKey == "" {
		if keyPath := os.Getenv("GITHUB_PRIVATE_KEY_PATH"); keyPath != "" {
			keyData, err := os.ReadFile(keyPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read GitHub private key from %s: %w", keyPath, err)
			}
			githubPrivateKey = string(keyData)
		}
	}

	// Set defaults for optional config.
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
		sprinklerURL = "wss://hook.g.robot-army.dev/ws"
	}

	cfg := &config.ServerConfig{
		DataDir:              dataDir,
		SlackToken:           os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret:   os.Getenv("SLACK_SIGNING_SECRET"),
		GitHubAppID:          os.Getenv("GITHUB_APP_ID"),
		GitHubPrivateKey:     githubPrivateKey,
		GitHubInstallationID: os.Getenv("GITHUB_INSTALLATION_ID"),
		SprinklerURL:         sprinklerURL,
	}

	// Validate required fields
	if cfg.SlackToken == "" {
		return nil, errors.New("missing required environment variable: SLACK_BOT_TOKEN")
	}
	if cfg.SlackSigningSecret == "" {
		return nil, errors.New("missing required environment variable: SLACK_SIGNING_SECRET")
	}
	if cfg.GitHubAppID == "" {
		return nil, errors.New("missing required environment variable: GITHUB_APP_ID")
	}
	if cfg.GitHubPrivateKey == "" {
		return nil, errors.New("missing required environment variable: GITHUB_PRIVATE_KEY or GITHUB_PRIVATE_KEY_PATH")
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
