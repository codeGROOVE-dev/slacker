// Package main implements a standalone OAuth registration service for Slack.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codeGROOVE-dev/gsm"
	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/gorilla/mux"
)

func main() {
	// Configure logging
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
	})
	slog.SetDefault(slog.New(logHandler))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := loadConfig(ctx)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize Slack manager
	slackManager := slack.NewManager(cfg.SlackSigningSecret)

	// Initialize OAuth handler
	oauthHandler := slack.NewOAuthHandler(slackManager, cfg.SlackClientID, cfg.SlackClientSecret)

	// Setup routes
	router := mux.NewRouter()
	router.Use(securityHeadersMiddleware)

	// Health check
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}).Methods("GET")

	// OAuth endpoints
	router.HandleFunc("/", oauthHandler.HandleInstall).Methods("GET")
	router.HandleFunc("/install", oauthHandler.HandleInstall).Methods("GET")
	router.HandleFunc("/oauth/callback", oauthHandler.HandleCallback).Methods("GET")
	router.HandleFunc("/debug", oauthHandler.HandleDebug).Methods("GET")

	// Determine port
	port := os.Getenv("PORT")
	if port == "" {
		port = "9120"
	}

	// Start server
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Info("received shutdown signal",
			"signal", sig.String())
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown failed", "error", err)
		}
	}()

	slog.Info("starting Slack registration service",
		"port", port,
		"client_id", cfg.SlackClientID,
		"install_url", fmt.Sprintf("http://localhost:%s/install", port))

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped")
}

// Config holds the registrar configuration.
type Config struct {
	SlackClientID      string
	SlackClientSecret  string
	SlackSigningSecret string
}

// loadConfig loads configuration from environment and Secret Manager.
func loadConfig(ctx context.Context) (*Config, error) {
	getSecret := func(name string) string {
		// Try environment variable first
		if value := os.Getenv(name); value != "" {
			return value
		}

		// Try Secret Manager
		value, err := gsm.Fetch(ctx, name)
		if err != nil {
			slog.Warn("failed to fetch secret",
				"name", name,
				"error", err)
			return ""
		}
		return value
	}

	cfg := &Config{
		SlackClientID:      os.Getenv("SLACK_CLIENT_ID"),
		SlackClientSecret:  getSecret("SLACK_CLIENT_SECRET"),
		SlackSigningSecret: getSecret("SLACK_SIGNING_SECRET"),
	}

	// Validate required fields
	if cfg.SlackClientID == "" {
		return nil, fmt.Errorf("missing required: SLACK_CLIENT_ID")
	}
	if cfg.SlackClientSecret == "" {
		return nil, fmt.Errorf("missing required: SLACK_CLIENT_SECRET")
	}
	if cfg.SlackSigningSecret == "" {
		return nil, fmt.Errorf("missing required: SLACK_SIGNING_SECRET")
	}

	slog.Info("configuration loaded",
		"client_id", cfg.SlackClientID,
		"has_client_secret", cfg.SlackClientSecret != "",
		"has_signing_secret", cfg.SlackSigningSecret != "")

	return cfg, nil
}

// securityHeadersMiddleware adds security headers to all responses.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}
