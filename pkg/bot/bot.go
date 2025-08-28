package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/gorilla/websocket"
)

// Coordinator coordinates between GitHub, Slack, and notifications.
type Coordinator struct {
	slack         *slack.Client
	github        *github.Client
	stateManager  *state.Manager
	configManager *config.Manager
	notifier      *notify.Manager
	sprinklerURL  string
	wsConn        *websocket.Conn
}

// New creates a new bot coordinator.
func New(
	ctx context.Context,
	slackClient *slack.Client,
	githubClient *github.Client,
	stateManager *state.Manager,
	configManager *config.Manager,
	notifier *notify.Manager,
	sprinklerURL string,
) *Coordinator {
	c := &Coordinator{
		slack:         slackClient,
		github:        githubClient,
		stateManager:  stateManager,
		configManager: configManager,
		notifier:      notifier,
		sprinklerURL:  sprinklerURL,
	}

	// Set GitHub client in config manager.
	configManager.SetGitHubClient(githubClient.GetClient())

	return c
}

// Run starts the bot coordinator.
func (c *Coordinator) Run(ctx context.Context) error {
	slog.Info("starting bot coordinator")

	var reconnectMu sync.Mutex
	reconnectCount := 0

	for {
		select {
		case <-ctx.Done():
			slog.Info("bot coordinator shutting down")
			if c.wsConn != nil {
				if err := c.wsConn.Close(); err != nil {
					slog.Error("failed to close WebSocket", "error", err)
				}
			}
			return ctx.Err()
		default:
		}

		// Connect with exponential backoff
		err := retry.Do(
			func() error {
				reconnectMu.Lock()
				defer reconnectMu.Unlock()

				if c.wsConn != nil {
					if err := c.wsConn.Close(); err != nil {
						slog.Debug("failed to close existing WebSocket", "error", err)
					}
				}

				if err := c.connectToSprinkler(ctx); err != nil {
					slog.Warn("failed to connect to sprinkler, retrying", "error", err)
					return err
				}

				reconnectCount++
				if reconnectCount > 1 {
					slog.Info("reconnected to sprinkler", "attempt", reconnectCount)
				}
				return nil
			},
			retry.Attempts(10),
			retry.Delay(time.Second),
			retry.MaxDelay(2*time.Minute),
			retry.DelayType(retry.BackOffDelay),
			retry.LastErrorOnly(true),
			retry.Context(ctx),
		)
		if err != nil {
			return fmt.Errorf("failed to connect to sprinkler after retries: %w", err)
		}

		// Read messages until connection fails
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			var msg SprinklerMessage
			if err := c.wsConn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
				slog.Debug("failed to set read deadline", "error", err)
			}

			if err := c.wsConn.ReadJSON(&msg); err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					slog.Info("WebSocket closed normally")
					return nil
				}
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Warn("WebSocket unexpected close, will reconnect", "error", err)
				} else {
					slog.Warn("failed to read WebSocket message, will reconnect", "error", err)
				}
				break // Break inner loop to reconnect
			}

			// Process the event asynchronously
			go func(msg SprinklerMessage) {
				if err := c.processEventSafely(ctx, msg); err != nil {
					slog.Error("error processing event", "error", err, "event", msg.Event)
				}
			}(msg)
		}
	}
}

// connectToSprinkler connects to the sprinkler WebSocket hub.
func (c *Coordinator) connectToSprinkler(ctx context.Context) error {
	slog.Info("connecting to sprinkler", "url", c.sprinklerURL)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}

	conn, resp, err := dialer.DialContext(ctx, c.sprinklerURL, nil)
	if err != nil {
		if resp != nil {
			slog.Error("WebSocket connection failed", "status", resp.StatusCode)
			if err := resp.Body.Close(); err != nil {
				slog.Debug("failed to close response body", "error", err)
			}
		}
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	if resp != nil {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("failed to close response body", "error", err)
		}
	}

	// Set connection parameters
	conn.SetPingHandler(func(message string) error {
		slog.Debug("received ping from sprinkler")
		return conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(10*time.Second))
	})

	conn.SetPongHandler(func(string) error {
		slog.Debug("received pong from sprinkler")
		return nil
	})

	c.wsConn = conn
	slog.Info("successfully connected to sprinkler")

	// Start ping ticker to keep connection alive
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
					slog.Debug("failed to send ping", "error", err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

// SprinklerMessage represents a message from sprinkler.
type SprinklerMessage struct {
	Event   string          `json:"event"`
	Repo    string          `json:"repo"`
	Payload json.RawMessage `json:"payload"`
}

// processEventSafely processes a GitHub webhook event with error recovery.
func (c *Coordinator) processEventSafely(ctx context.Context, msg SprinklerMessage) (err error) {
	// Recover from panics
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered in processEvent: %v", r)
			slog.Error("panic recovered in processEvent", "panic", r)
		}
	}()

	return c.processEvent(ctx, msg)
}

// processEvent processes a GitHub webhook event.
func (c *Coordinator) processEvent(ctx context.Context, msg SprinklerMessage) error {
	slog.Info("processing event", "event", msg.Event, "repo", msg.Repo)

	// Parse repo owner and name.
	parts := strings.Split(msg.Repo, "/")
	if len(parts) != 2 {
		slog.Warn("invalid repo format", "repo", msg.Repo)
		return fmt.Errorf("invalid repo format: %s", msg.Repo)
	}
	owner := parts[0]
	repo := parts[1]

	if owner == "" || repo == "" {
		slog.Warn("empty owner or repo name", "owner", owner, "repo", repo)
		return fmt.Errorf("empty owner or repo name")
	}

	// Load config for this org if not already loaded.
	if _, exists := c.configManager.GetConfig(owner); !exists {
		if err := c.configManager.LoadConfig(ctx, owner); err != nil {
			slog.Warn("failed to load config for org", "org", owner, "error", err)
		}
	}

	// Handle different event types.
	switch msg.Event {
	case "pull_request":
		c.handlePullRequestEvent(ctx, owner, repo, msg.Payload)
	case "pull_request_review":
		c.handlePullRequestReviewEvent(ctx, owner, repo, msg.Payload)
	case "check_run", "check_suite":
		c.handleCheckEvent(ctx, owner, repo, msg.Payload)
	case "push":
		// Check if this is a push to .github repo.
		if repo == ".github" {
			c.handleConfigUpdate(ctx, owner)
		}
	default:
		slog.Debug("unhandled event type", "event", msg.Event)
	}

	return nil
}

// handlePullRequestEvent handles pull request events.
func (c *Coordinator) handlePullRequestEvent(ctx context.Context, owner, repo string, payload json.RawMessage) {
	var event struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			User   struct {
				Login string `json:"login"`
			} `json:"user"`
			HTMLURL string `json:"html_url"`
		} `json:"pull_request"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		slog.Warn("failed to unmarshal PR event", "error", err)
		return
	}

	slog.Info("PR event", "owner", owner, "repo", repo, "number", event.Number, "action", event.Action)

	// Get channels for this repo.
	channels := c.configManager.GetChannelsForRepo(owner, repo)
	if len(channels) == 0 {
		slog.Debug("no channels configured", "owner", owner, "repo", repo)
		return
	}

	// Get PR state.
	prState, blockedOn, err := c.github.GetPRState(ctx, owner, repo, event.Number)
	if err != nil {
		slog.Warn("failed to get PR state", "error", err)
		return
	}

	// For now, use a default workspace ID.
	// In production, this would map channels to workspaces.
	workspaceID := "default"

	// Update or create PR state.
	pr := &state.PRState{
		Owner:       owner,
		Repo:        repo,
		Number:      event.Number,
		Title:       event.PullRequest.Title,
		Author:      event.PullRequest.User.Login,
		State:       prState,
		BlockedOn:   blockedOn,
		LastUpdated: time.Now(),
	}

	// Check if we already have a thread for this PR.
	existingPR, exists := c.stateManager.GetPRState(workspaceID, owner, repo, event.Number)
	if exists {
		pr.ThreadTS = existingPR.ThreadTS
		pr.ChannelID = existingPR.ChannelID
	}

	// Handle based on action.
	switch event.Action {
	case "opened", "reopened":
		// Create threads in configured channels.
		for _, channel := range channels {
			if pr.ThreadTS != "" {
				continue
			}
			// Create new thread.
			threadTS, err := c.createPRThread(ctx, channel, owner, repo, event.Number, event.PullRequest)
			if err != nil {
				slog.Warn("failed to create thread", "channel", channel, "error", err)
				continue
			}
			pr.ThreadTS = threadTS
			pr.ChannelID = channel
			slog.Info("created thread", "channel", channel, "owner", owner, "repo", repo, "number", event.Number)
		}

	case "closed":
		// Update state in existing thread.
		if pr.ThreadTS != "" {
			if err := c.notifier.UpdateThreadReaction(ctx, pr.ChannelID, pr.ThreadTS, prState); err != nil {
				slog.Warn("failed to update reaction", "error", err)
			}
		}

	case "synchronize", "edited":
		// Update state.
		if pr.ThreadTS != "" {
			if err := c.notifier.UpdateThreadReaction(ctx, pr.ChannelID, pr.ThreadTS, prState); err != nil {
				slog.Warn("failed to update reaction", "error", err)
			}
		}
	}

	// Save PR state.
	c.stateManager.SetPRState(workspaceID, pr)

	// Check if we need to notify blocked users.
	for _, userID := range blockedOn {
		// In production, map GitHub username to Slack user ID.
		// Then update their app home view.
		slog.Info("PR blocked on user", "owner", owner, "repo", repo, "number", event.Number, "user", userID)
		// Would call: c.updateUserHome(ctx, workspaceID, slackUserID)
	}
}

// handlePullRequestReviewEvent handles PR review events.
func (c *Coordinator) handlePullRequestReviewEvent(ctx context.Context, owner, repo string, payload json.RawMessage) {
	var event struct {
		Action string `json:"action"`
		Review struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
			State string `json:"state"`
		} `json:"review"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		slog.Warn("failed to unmarshal review event", "error", err)
		return
	}

	workspaceID := "default"
	pr, exists := c.stateManager.GetPRState(workspaceID, owner, repo, event.PullRequest.Number)
	if !exists {
		return
	}

	// Update thread with review status.
	if pr.ThreadTS != "" && event.Action == "submitted" {
		message := fmt.Sprintf("@%s reviewed the PR", event.Review.User.Login)
		if event.Review.State == "approved" {
			message += " ✅"
		} else if event.Review.State == "changes_requested" {
			message += " 🔧"
		}
		if err := c.notifier.SendThreadUpdate(ctx, pr.ChannelID, pr.ThreadTS, message); err != nil {
			slog.Warn("failed to send thread update", "error", err)
		}
	}

	// Update PR state.
	prState, blockedOn, err := c.github.GetPRState(ctx, owner, repo, event.PullRequest.Number)
	if err == nil {
		pr.State = prState
		pr.BlockedOn = blockedOn
		pr.LastUpdated = time.Now()
		c.stateManager.SetPRState(workspaceID, pr)

		// Update reaction.
		if pr.ThreadTS != "" {
			if err := c.notifier.UpdateThreadReaction(ctx, pr.ChannelID, pr.ThreadTS, prState); err != nil {
				slog.Warn("failed to update reaction", "error", err)
			}
		}
	}
}

// handleCheckEvent handles check run/suite events.
func (c *Coordinator) handleCheckEvent(ctx context.Context, owner, repo string, payload json.RawMessage) {
	// Parse to get PR number.
	// This is simplified - in production, we'd need to map commits to PRs.
	slog.Debug("received check event", "owner", owner, "repo", repo)
}

// handleConfigUpdate handles updates to org config.
func (c *Coordinator) handleConfigUpdate(ctx context.Context, owner string) {
	slog.Info("reloading config", "org", owner)
	if err := c.configManager.ReloadConfig(ctx, owner); err != nil {
		slog.Warn("failed to reload config", "error", err)
	}
}

// createPRThread creates a new thread in Slack for a PR.
func (c *Coordinator) createPRThread(ctx context.Context, channel, owner, repo string, number int, pr struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL string `json:"html_url"`
},
) (string, error) {
	// Get prefix for this org.
	prefix := c.configManager.GetPrefix(owner)

	// Format message.
	text := fmt.Sprintf(
		"%s %s • <%s|%s/%s#%d> by @%s",
		prefix,
		pr.Title,
		pr.HTMLURL,
		owner,
		repo,
		number,
		pr.User.Login,
	)

	// Create thread.
	threadTS, err := c.slack.PostThread(ctx, channel, text, nil)
	if err != nil {
		return "", fmt.Errorf("failed to post thread: %w", err)
	}

	// Add initial reaction based on state.
	prState, _, err := c.github.GetPRState(ctx, owner, repo, number)
	if err == nil {
		if err := c.slack.UpdateReactions(ctx, channel, threadTS, prState); err != nil {
			slog.Warn("failed to add initial reaction", "error", err)
		}
	}

	return threadTS, nil
}
