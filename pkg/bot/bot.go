// Package bot implements the coordination logic between GitHub, Slack, and notifications.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
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
	configManager.SetGitHubClient(githubClient.Client())

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

		// Track consecutive 403 errors
		var consecutive403s int

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
					// Check if this is a 403 error
					if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "forbidden") {
						consecutive403s++
						if consecutive403s >= 2 {
							// After 2 consecutive 403s, force a token refresh
							slog.Warn("multiple 403 errors, forcing token refresh", "consecutive_403s", consecutive403s)
							if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
								slog.Error("failed to refresh token after 403s", "error", refreshErr)
							}
							consecutive403s = 0 // Reset counter after refresh attempt
						}
					} else {
						consecutive403s = 0 // Reset on non-403 error
					}

					slog.Warn("failed to connect to sprinkler, retrying", "error", err)
					return err
				}

				consecutive403s = 0 // Reset on success
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
			retry.MaxJitter(time.Second),
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
			// Set read deadline - this will be reset by ping/pong handlers
			if err := c.wsConn.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
				slog.Debug("failed to set read deadline", "error", err)
			}

			if err := c.wsConn.ReadJSON(&msg); err != nil {
				// Check for specific close codes
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) {
					switch closeErr.Code {
					case websocket.CloseNormalClosure:
						slog.Info("WebSocket closed normally by server")
					case 1006: // Abnormal closure
						slog.Error("WebSocket abnormal closure - possible causes:",
							"error", err,
							"causes", "1) Server crashed or restarted, 2) Network interruption, 3) Authentication expired mid-connection, 4) Server rejected message")
						// Try to refresh token in case it's an auth issue
						if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
							slog.Error("failed to refresh token after abnormal closure", "error", refreshErr)
						}
					case 1008: // Policy Violation
						slog.Warn("WebSocket closed due to policy violation (auth/permission failure), refreshing token",
							"error", err,
							"text", closeErr.Text)
						if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
							slog.Error("failed to refresh token after policy violation", "error", refreshErr)
						}
					case 1003: // Unsupported Data
						slog.Error("WebSocket closed due to unsupported data format",
							"error", err,
							"text", closeErr.Text,
							"hint", "Check subscription message format")
					case 1011: // Internal Server Error
						slog.Error("WebSocket closed due to server error",
							"error", err,
							"text", closeErr.Text)
					default:
						slog.Warn("WebSocket closed with code",
							"code", closeErr.Code,
							"text", closeErr.Text,
							"error", err)
					}
				} else if errors.Is(err, os.ErrDeadlineExceeded) {
					slog.Warn("WebSocket read timeout, will reconnect", "error", err)
				} else if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Warn("WebSocket unexpected close, will reconnect", "error", err)
				} else {
					slog.Warn("failed to read WebSocket message, will reconnect",
						"error", err,
						"type", fmt.Sprintf("%T", err))
				}
				break // Break inner loop to reconnect
			}

			// Successfully read a message, log it
			slog.Debug("received WebSocket message", "event", msg.Event, "repo", msg.Repo, "has_payload", len(msg.Payload) > 0)

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
		// Ensure TLS verification is enabled (this is default, but being explicit).
		TLSClientConfig: nil, // Uses default TLS config with verification
	}

	// Add GitHub token authentication header.
	headers := http.Header{}
	// Get fresh token from GitHub client
	githubToken := c.github.InstallationToken(ctx)
	if githubToken != "" {
		headers.Set("Authorization", "Bearer "+githubToken)
		// Log that we're using authentication (but not the token itself)
		slog.Info("using GitHub token for sprinkler authentication",
			"token_length", len(githubToken),
			"token_prefix", githubToken[:min(10, len(githubToken))]+"...",
			"token_suffix", "..."+githubToken[max(0, len(githubToken)-5):])
	} else {
		slog.Warn("no GitHub token available for sprinkler authentication - connection will likely fail")
	}

	// Add user-agent for easier debugging.
	headers.Set("User-Agent", "Slacker/1.0.0 (github.com/codeGROOVE-dev/slacker)")

	// Log the connection attempt details and all headers
	authHeader := headers.Get("Authorization")
	slog.Info("attempting WebSocket connection",
		"url", c.sprinklerURL,
		"has_auth", githubToken != "",
		"auth_header_length", len(authHeader),
		"auth_header_present", authHeader != "",
		"user_agent", headers.Get("User-Agent"))

	conn, resp, err := dialer.DialContext(ctx, c.sprinklerURL, headers)

	// Log response details even on success for debugging
	if resp != nil {
		slog.Debug("WebSocket handshake response received",
			"status_code", resp.StatusCode,
			"status", http.StatusText(resp.StatusCode),
			"headers", resp.Header)
	}

	if err != nil {
		if resp != nil {
			// Provide detailed error information based on status code
			var errorHint string
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				errorHint = "Authentication failed. Check that your GitHub token is valid and has the correct permissions."
			case http.StatusForbidden:
				// Try to read the response body for more details
				var bodyContent string
				if resp.Body != nil {
					bodyBytes, readErr := io.ReadAll(resp.Body)
					if readErr == nil {
						bodyContent = string(bodyBytes)
					}
				}

				// Log the token details for debugging
				slog.Warn("received 403 forbidden from sprinkler, will force token refresh",
					"token_was_sent", githubToken != "",
					"token_length", len(githubToken),
					"response_body", bodyContent)

				// Force a token refresh on 403
				slog.Info("forcing GitHub token refresh due to 403 error")
				if err := c.github.RefreshToken(ctx); err != nil {
					slog.Error("failed to refresh GitHub token", "error", err)
				} else {
					slog.Info("GitHub token refreshed successfully, will retry connection")
				}

				errorHint = "Access forbidden - token may have expired. Refreshing token and retrying..."
				if bodyContent != "" {
					errorHint += "\n  Response: " + bodyContent
				}
			case http.StatusNotFound:
				errorHint = "WebSocket endpoint not found. Check that SPRINKLER_URL is correct (current: " + c.sprinklerURL + ")"
			case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
				errorHint = "Server error. The sprinkler service may be down or experiencing issues."
			default:
				errorHint = fmt.Sprintf("Unexpected status code %d. Check sprinkler service logs for details.", resp.StatusCode)
			}

			slog.Error("WebSocket connection failed",
				"status_code", resp.StatusCode,
				"status_text", http.StatusText(resp.StatusCode),
				"url", c.sprinklerURL,
				"hint", errorHint)

			if err := resp.Body.Close(); err != nil {
				slog.Debug("failed to close response body", "error", err)
			}
		} else {
			// No response or WebSocket handshake error
			errMsg := err.Error()
			var hint string

			// Check for common WebSocket errors
			switch {
			case strings.Contains(errMsg, "bad handshake"):
				hint = "WebSocket handshake failed. Possible causes:\n" +
					"  1. Server returned non-WebSocket response (check if URL supports WebSocket)\n" +
					"  2. Authentication token was rejected during upgrade\n" +
					"  3. Server is not a WebSocket endpoint\n" +
					"  Debug: Try connecting with a WebSocket client like 'wscat' to test the endpoint"
			case strings.Contains(errMsg, "connection refused"):
				hint = "Connection refused. The server may be down or the port may be blocked."
			case strings.Contains(errMsg, "no such host"):
				hint = "DNS resolution failed. Check that the hostname is correct."
			case strings.Contains(errMsg, "timeout"):
				hint = "Connection timeout. Check network connectivity and firewall rules."
			default:
				hint = "Check network connectivity and verify the WebSocket URL is correct."
			}

			slog.Error("WebSocket connection failed",
				"url", c.sprinklerURL,
				"error", errMsg,
				"has_auth", githubToken != "",
				"hint", hint)
		}
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	if resp != nil {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("failed to close response body", "error", err)
		}
	}

	// Set connection parameters with more aggressive timeouts
	if err := conn.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
		slog.Warn("failed to set read deadline", "error", err)
	}
	conn.SetPingHandler(func(message string) error {
		slog.Debug("received ping from sprinkler", "message", message)
		// Reset read deadline on ping
		if err := conn.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
			slog.Warn("failed to set read deadline on ping", "error", err)
		}
		if err := conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(10*time.Second)); err != nil {
			slog.Error("failed to send pong", "error", err)
			return err
		}
		return nil
	})

	conn.SetPongHandler(func(message string) error {
		slog.Debug("received pong from sprinkler", "message", message)
		// Reset read deadline on pong
		if err := conn.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
			slog.Warn("failed to set read deadline on pong", "error", err)
		}
		return nil
	})

	// Set close handler to log close reasons
	conn.SetCloseHandler(func(code int, text string) error {
		slog.Info("WebSocket close received", "code", code, "text", text)
		return nil
	})

	c.wsConn = conn
	slog.Info("successfully connected to sprinkler",
		"url", c.sprinklerURL,
		"local_addr", conn.LocalAddr().String(),
		"remote_addr", conn.RemoteAddr().String())

	// Send subscription message to sprinkler
	// The server expects us to tell it what repos we want to subscribe to
	// Try to get list of repos from GitHub if possible
	repos := []string{"*"} // Default to all repos

	// Log what we're subscribing to
	slog.Info("preparing subscription",
		"repos", repos,
		"note", "Using wildcard '*' for all repos - server may require specific repo names")

	subscription := map[string]interface{}{
		"type": "subscribe",
		"events": []string{
			"pull_request",
			"pull_request_review",
			"check_run",
			"check_suite",
			"push",
		},
		"repos": repos,
	}

	if err := conn.WriteJSON(subscription); err != nil {
		slog.Error("failed to send subscription", "error", err)
		return fmt.Errorf("failed to send subscription: %w", err)
	}
	slog.Info("sent subscription to sprinkler", "subscription", subscription)

	// Try to read subscription response (but don't fail if none comes)
	// Some servers might not send a response, just start sending events
	responseChan := make(chan map[string]interface{}, 1)
	errorChan := make(chan error, 1)

	go func() {
		// Set a short deadline for the response
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			slog.Debug("failed to set read deadline for subscription response", "error", err)
		}
		var response map[string]interface{}
		if err := conn.ReadJSON(&response); err != nil {
			// Check if it's a timeout (expected if server doesn't send response)
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				slog.Debug("no subscription response received (server may not send one)")
				errorChan <- nil // Not an error, some servers don't respond
				return
			}
			// Check for close errors (these are real problems)
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				switch closeErr.Code {
				case 1006:
					errorChan <- errors.New("server closed connection without response - likely authentication or permission issue")
				case 1008:
					errorChan <- errors.New("server rejected subscription - policy violation (check GitHub App permissions)")
				case 1003:
					errorChan <- errors.New("server rejected subscription - invalid data format")
				default:
					errorChan <- fmt.Errorf("server closed connection with code %d: %s", closeErr.Code, closeErr.Text)
				}
				return
			}
			// Other errors might just mean no response was sent
			slog.Debug("no subscription response", "error", err)
			errorChan <- nil
		} else {
			responseChan <- response
		}
	}()

	// Wait for response or timeout
	select {
	case response := <-responseChan:
		// Check subscription response
		if responseType, ok := response["type"].(string); ok {
			switch responseType {
			case "error":
				errorMsg := "unknown error"
				if msg, ok := response["message"].(string); ok {
					errorMsg = msg
				}
				if code, ok := response["code"].(float64); ok {
					if code == 403 {
						slog.Error("subscription rejected - permission denied",
							"message", errorMsg,
							"hint", "Check that your GitHub App has access to the requested repositories")
						// Force token refresh on permission denied
						if refreshErr := c.github.RefreshToken(ctx); refreshErr != nil {
							slog.Error("failed to refresh token after permission denied", "error", refreshErr)
						}
						return fmt.Errorf("subscription rejected with 403: %s", errorMsg)
					}
				}
				return fmt.Errorf("subscription rejected: %s", errorMsg)
			case "subscribed", "ok", "success":
				slog.Info("subscription confirmed", "response", response)
			default:
				slog.Debug("received subscription response", "type", responseType, "response", response)
			}
		} else {
			slog.Debug("received subscription response without type", "response", response)
		}
	case err := <-errorChan:
		if err != nil {
			return err
		}
		slog.Info("subscription sent, no response received (proceeding anyway)")
	case <-ctx.Done():
		return errors.New("context canceled while waiting for subscription response")
	case <-time.After(5 * time.Second):
		slog.Info("no subscription response after 5s (proceeding anyway)")
	}

	// Reset read deadline for normal operation
	if err := conn.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
		slog.Warn("failed to reset read deadline after subscription", "error", err)
	}

	// Start ping ticker to keep connection alive - more frequent pings
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				slog.Debug("sending ping to keep connection alive")
				deadline := time.Now().Add(10 * time.Second)
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, deadline); err != nil {
					slog.Warn("failed to send ping", "error", err)
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
	// Skip empty messages (likely subscription confirmations or keepalives)
	if msg.Event == "" && msg.Repo == "" {
		slog.Debug("received empty message from sprinkler, likely acknowledgment")
		return nil
	}

	// Skip messages without repo information
	if msg.Repo == "" {
		slog.Debug("received message without repo", "event", msg.Event)
		return nil
	}

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
		return errors.New("empty owner or repo name")
	}

	// Load config for this org if not already loaded.
	if _, exists := c.configManager.Config(owner); !exists {
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
	channels := c.configManager.ChannelsForRepo(owner, repo)
	if len(channels) == 0 {
		slog.Debug("no channels configured", "owner", owner, "repo", repo)
		return
	}

	// Get PR state.
	prState, blockedOn, err := c.github.PRState(ctx, owner, repo, event.Number)
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
	existingPR, exists := c.stateManager.PRState(workspaceID, owner, repo, event.Number)
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
	default:
		// Other PR actions are not handled
		slog.Debug("unhandled PR action", "action", event.Action)
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
	pr, exists := c.stateManager.PRState(workspaceID, owner, repo, event.PullRequest.Number)
	if !exists {
		return
	}

	// Update thread with review status.
	if pr.ThreadTS != "" && event.Action == "submitted" {
		message := fmt.Sprintf("@%s reviewed the PR", event.Review.User.Login)
		switch event.Review.State {
		case "approved":
			message += " ✅"
		case "changes_requested":
			message += " 🔧"
		default:
			// Other review states (commented, dismissed, etc.)
			message += fmt.Sprintf(" (%s)", event.Review.State)
		}
		if err := c.notifier.SendThreadUpdate(ctx, pr.ChannelID, pr.ThreadTS, message); err != nil {
			slog.Warn("failed to send thread update", "error", err)
		}
	}

	// Update PR state.
	prState, blockedOn, err := c.github.PRState(ctx, owner, repo, event.PullRequest.Number)
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
func (*Coordinator) handleCheckEvent(_ context.Context, owner, repo string, _ json.RawMessage) {
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
	prefix := c.configManager.Prefix(owner)

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
	prState, _, err := c.github.PRState(ctx, owner, repo, number)
	if err == nil {
		if err := c.slack.UpdateReactions(ctx, channel, threadTS, prState); err != nil {
			slog.Warn("failed to add initial reaction", "error", err)
		}
	}

	return threadTS, nil
}
