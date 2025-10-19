// Package main implements a standalone OAuth registration service for Slack.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/codeGROOVE-dev/gsm"
	"github.com/codeGROOVE-dev/slacker/internal/slack"
	"github.com/gorilla/mux"
	"golang.org/x/time/rate"
)

// Constants for server configuration.
const (
	oauthRateLimiterBurst = 20               // Maximum burst of OAuth requests allowed
	serverReadTimeout     = 15 * time.Second // Maximum duration for reading the entire request
	serverWriteTimeout    = 15 * time.Second // Maximum duration before timing out writes of the response
)

func main() {
	// Configure logging with PID for instance identification
	pid := os.Getpid()
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
	// Create logger with PID as a default attribute
	logger := slog.New(logHandler).With("pid", pid)
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())

	// Load configuration
	cfg, err := loadConfig(ctx)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		cancel()
		os.Exit(1)
	}

	// Initialize Slack manager (signing secret not needed for OAuth-only service)
	slackManager := slack.NewManager("")

	// Initialize OAuth handler
	oauthHandler := slack.NewOAuthHandler(slackManager, cfg.SlackClientID, cfg.SlackClientSecret)

	// Setup routes
	router := mux.NewRouter()
	router.Use(securityHeadersMiddleware)

	// Rate limiter for OAuth endpoints: 10 requests per second, burst of oauthRateLimiterBurst
	// This prevents abuse while allowing legitimate installation flows
	oauthLimiter := rate.NewLimiter(10, oauthRateLimiterBurst)

	// Health check
	router.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "ok"); err != nil {
			slog.Error("failed to write health response", "error", err)
		}
	}).Methods("GET")

	// OAuth endpoints with rate limiting
	router.Handle("/", rateLimitMiddleware(oauthLimiter)(http.HandlerFunc(oauthHandler.HandleInstall))).Methods("GET")
	router.Handle("/install", rateLimitMiddleware(oauthLimiter)(http.HandlerFunc(oauthHandler.HandleInstall))).Methods("GET")
	router.Handle("/oauth/callback", rateLimitMiddleware(oauthLimiter)(http.HandlerFunc(oauthHandler.HandleCallback))).Methods("GET")

	// Debug endpoint - DO NOT EXPOSE IN PRODUCTION
	// Remove this endpoint entirely or protect with strong authentication
	// router.HandleFunc("/debug", oauthHandler.HandleDebug).Methods("GET")

	// Determine port
	port := os.Getenv("PORT")
	if port == "" {
		port = "9120"
	}

	// Start server
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
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
		cancel()
		os.Exit(1)
	}

	cancel()
	slog.Info("server stopped")
}

// Config holds the registrar configuration.
type Config struct {
	SlackClientID     string
	SlackClientSecret string
}

// loadConfig loads configuration from environment and Secret Manager.
func loadConfig(ctx context.Context) (*Config, error) {
	getSecret := func(name string) string {
		// Try environment variable first
		if value := os.Getenv(name); value != "" {
			slog.Info("using environment variable", "name", name)
			return value
		}

		// Try Secret Manager
		slog.Info("fetching from Secret Manager", "name", name)
		value, err := gsm.Fetch(ctx, name)
		if err != nil {
			slog.Warn("failed to fetch secret from GSM",
				"name", name,
				"error", err)
			return ""
		}
		slog.Info("successfully fetched from Secret Manager", "name", name)
		return value
	}

	cfg := &Config{
		SlackClientID:     getSecret("SLACK_CLIENT_ID"),
		SlackClientSecret: getSecret("SLACK_CLIENT_SECRET"),
	}

	// Validate required fields
	if cfg.SlackClientID == "" {
		return nil, errors.New("missing required: SLACK_CLIENT_ID (env var or GSM)")
	}
	if cfg.SlackClientSecret == "" {
		return nil, errors.New("missing required: SLACK_CLIENT_SECRET (env var or GSM)")
	}

	slog.Info("configuration loaded successfully",
		"client_id", cfg.SlackClientID,
		"has_client_secret", cfg.SlackClientSecret != "")

	return cfg, nil
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
