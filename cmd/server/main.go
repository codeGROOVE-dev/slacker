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

	// Load configuration from environment.
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		cancel()
		os.Exit(1)
	}

	// Initialize state manager with file persistence.
	stateManager := state.New(cfg.DataDir)

	// Initialize config manager for repo configs.
	configManager := config.New(ctx)

	// Initialize GitHub client.
	githubClient, err := github.New(ctx, cfg.GitHubAppID, cfg.GitHubPrivateKey, cfg.GitHubInstallationID)
	if err != nil {
		slog.Error("failed to initialize GitHub client", "error", err)
		cancel()
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

	// Setup HTTP routes.
	router := mux.NewRouter()
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
	cfg := &config.ServerConfig{
		DataDir:              getEnvOrDefault("DATA_DIR", "./data"),
		SlackToken:           os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret:   os.Getenv("SLACK_SIGNING_SECRET"),
		GitHubAppID:          os.Getenv("GITHUB_APP_ID"),
		GitHubPrivateKey:     os.Getenv("GITHUB_PRIVATE_KEY"),
		GitHubInstallationID: os.Getenv("GITHUB_INSTALLATION_ID"),
		SprinklerURL:         getEnvOrDefault("SPRINKLER_URL", "wss://hook.g.robot-army.dev/ws"),
	}

	if cfg.SlackToken == "" {
		return nil, errMissingEnvVar("SLACK_BOT_TOKEN")
	}
	if cfg.SlackSigningSecret == "" {
		return nil, errMissingEnvVar("SLACK_SIGNING_SECRET")
	}
	if cfg.GitHubAppID == "" {
		return nil, errMissingEnvVar("GITHUB_APP_ID")
	}
	if cfg.GitHubPrivateKey == "" {
		return nil, errMissingEnvVar("GITHUB_PRIVATE_KEY")
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func errMissingEnvVar(name string) error {
	return &missingEnvVarError{name: name}
}

type missingEnvVarError struct {
	name string
}

func (e *missingEnvVarError) Error() string {
	return "missing required environment variable: " + e.name
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("failed to write health response", "error", err)
	}
}
