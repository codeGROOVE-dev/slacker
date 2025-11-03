package slack

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/slack-go/slack"
)

// WorkspaceStorer stores workspace credentials after OAuth completion.
type WorkspaceStorer interface {
	StoreWorkspace(ctx context.Context, metadata *WorkspaceMetadata, token string) error
}

// OAuthExchanger exchanges OAuth authorization codes for access tokens.
type OAuthExchanger interface {
	ExchangeCode(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error)
}

// slackOAuthExchanger is the default implementation using slack-go/slack.
type slackOAuthExchanger struct{}

func (s *slackOAuthExchanger) ExchangeCode(ctx context.Context, clientID, clientSecret, code string) (*slack.OAuthV2Response, error) {
	return slack.GetOAuthV2ResponseContext(ctx, &http.Client{}, clientID, clientSecret, code, "")
}

// OAuthHandler handles the OAuth callback from Slack.
type OAuthHandler struct {
	store        WorkspaceStorer // For OAuth callback storage
	exchanger    OAuthExchanger  // For OAuth code exchange
	manager      *Manager        // For debug listing (optional)
	clientID     string
	clientSecret string
}

// NewOAuthHandler creates a new OAuth handler.
// If manager is passed (implements WorkspaceStorer), it's used for both storage and debug listing.
func NewOAuthHandler(manager *Manager, clientID, clientSecret string) *OAuthHandler {
	return &OAuthHandler{
		store:        manager,
		exchanger:    &slackOAuthExchanger{},
		manager:      manager,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// HandleCallback handles the OAuth callback from Slack.
func (h *OAuthHandler) HandleCallback(writer http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	// SECURITY: Verify CSRF token via state parameter to prevent OAuth hijacking attacks
	// Note: For Slack App Directory installations, users may arrive directly at /oauth/callback
	// without going through our /install page first. In this case, state will be empty.
	stateParam := req.URL.Query().Get("state")

	// If state parameter exists (non-empty), verify it matches our cookie
	if stateParam != "" {
		// Retrieve state from cookie
		cookie, err := req.Cookie("oauth_state")
		if err != nil {
			slog.Error("OAuth callback has state parameter but missing state cookie - possible CSRF attack",
				"error", err)
			http.Error(writer, "Invalid state parameter", http.StatusBadRequest)
			return
		}

		// Verify state matches
		if cookie.Value != stateParam {
			slog.Warn("OAuth state mismatch - possible CSRF attack",
				"cookie_state", cookie.Value[:min(len(cookie.Value), 10)]+"...",
				"param_state", stateParam[:min(len(stateParam), 10)]+"...")
			http.Error(writer, "Invalid state parameter", http.StatusBadRequest)
			return
		}

		// Clear the state cookie immediately after verification
		http.SetCookie(writer, &http.Cookie{
			Name:     "oauth_state",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})

		slog.Info("OAuth callback with state verification passed")
	} else {
		// Empty state - likely direct installation from Slack App Directory
		slog.Info("OAuth callback without state parameter - likely Slack App Directory installation")
	}

	// Extract authorization code
	code := req.URL.Query().Get("code")
	if code == "" {
		slog.Error("OAuth callback missing code parameter")
		http.Error(writer, "Missing code parameter", http.StatusBadRequest)
		return
	}

	errorParam := req.URL.Query().Get("error")
	if errorParam != "" {
		slog.Error("OAuth callback received error",
			"error", errorParam)
		http.Error(writer, fmt.Sprintf("OAuth error: %s", errorParam), http.StatusBadRequest)
		return
	}

	slog.Info("received OAuth callback",
		"code_prefix", code[:min(len(code), 10)]+"...")

	// Exchange code for token with retry
	var resp *slack.OAuthV2Response
	err := retry.Do(
		func() error {
			var err error
			resp, err = h.exchanger.ExchangeCode(ctx, h.clientID, h.clientSecret, code)
			if err != nil {
				slog.Warn("failed to exchange OAuth code for token, will retry",
					"error", err)
				return err
			}
			return nil
		},
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Error("failed to exchange OAuth code for token",
			"error", err)
		http.Error(writer, "Failed to complete OAuth flow", http.StatusInternalServerError)
		return
	}

	if !resp.Ok {
		slog.Error("OAuth token exchange returned not ok",
			"error", resp.Error)
		http.Error(writer, fmt.Sprintf("OAuth error: %s", resp.Error), http.StatusInternalServerError)
		return
	}

	// Extract workspace information
	teamID := resp.Team.ID
	teamName := resp.Team.Name
	botToken := resp.AccessToken
	botUserID := resp.BotUserID

	slog.Info("successfully exchanged OAuth code for token",
		"team_id", teamID,
		"team_name", teamName,
		"bot_user_id", botUserID,
		"scopes", resp.Scope)

	// Store workspace credentials
	metadata := &WorkspaceMetadata{
		TeamID:    teamID,
		TeamName:  teamName,
		BotUserID: botUserID,
	}

	if err := h.store.StoreWorkspace(ctx, metadata, botToken); err != nil {
		slog.Error("failed to store workspace credentials",
			"team_id", teamID,
			"team_name", teamName,
			"error", err)
		http.Error(writer, "Failed to store credentials", http.StatusInternalServerError)
		return
	}

	// Return success page
	h.writeSuccessPage(writer, teamName)

	slog.Info("OAuth installation completed successfully",
		"team_id", teamID,
		"team_name", teamName)
}

// writeSuccessPage renders the OAuth success page.
func (*OAuthHandler) writeSuccessPage(writer http.ResponseWriter, teamName string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(writer, `
<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Installation Complete - Ready to Review</title>
	<link rel="preconnect" href="https://fonts.googleapis.com">
	<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
	<link href="https://fonts.googleapis.com/css2?family=Passion+One:wght@700&display=swap" rel="stylesheet">
	<style>
		:root {
			--yellow: #ffe838;
			--cyan: #00d4dd;
			--black: #000000;
			--white: #ffffff;
			--gray: #999999;
		}
		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}
		body {
			font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
			background: var(--black);
			color: var(--white);
			min-height: 100vh;
			display: flex;
			align-items: center;
			justify-content: center;
			padding: 20px;
		}
		.success-container {
			max-width: 600px;
			background: var(--black);
			border: 8px solid var(--cyan);
			border-radius: 20px;
			padding: 60px 40px;
			text-align: center;
			box-shadow: 0 12px 40px rgba(0, 212, 221, 0.4);
		}
		@media (prefers-reduced-motion: no-preference) {
			.success-container {
				animation: slideIn 0.5s ease-out;
			}
			.checkmark {
				animation: bounce 0.8s ease-out;
			}
			.rocket {
				animation: float 2s ease-in-out infinite;
			}
		}
		@keyframes slideIn {
			from {
				opacity: 0;
				transform: translateY(-20px);
			}
			to {
				opacity: 1;
				transform: translateY(0);
			}
		}
		@keyframes bounce {
			0%%, 20%%, 50%%, 80%%, 100%% { transform: translateY(0); }
			40%% { transform: translateY(-20px); }
			60%% { transform: translateY(-10px); }
		}
		@keyframes float {
			0%%, 100%% { transform: translateY(0); }
			50%% { transform: translateY(-10px); }
		}
		.checkmark {
			font-size: 80px;
			color: var(--yellow);
			margin-bottom: 20px;
			display: block;
		}
		h1 {
			font-family: "Passion One", cursive;
			font-size: 48px;
			font-weight: 700;
			font-style: italic;
			color: var(--white);
			margin-bottom: 24px;
			line-height: 1.2;
		}
		h2 {
			color: var(--cyan);
			font-family: "Passion One", cursive;
			font-size: 56px;
			font-weight: 700;
			margin: 20px 0;
			line-height: 1.1;
		}
		p {
			font-size: 20px;
			line-height: 1.6;
			color: var(--white);
			margin-bottom: 16px;
		}
		.cta {
			font-size: 18px;
			font-weight: 600;
			color: var(--yellow);
			margin-top: 32px;
		}
		.cta a {
			color: var(--cyan);
			text-decoration: underline;
			text-decoration-thickness: 2px;
			text-underline-offset: 4px;
			font-weight: 700;
		}
		.cta a:hover,
		.cta a:focus {
			color: var(--yellow);
			outline: 2px solid var(--cyan);
			outline-offset: 4px;
			border-radius: 4px;
			padding: 2px 4px;
			text-decoration: none;
		}
		.rocket {
			font-size: 40px;
			margin: 0 8px;
		}
		.footer {
			font-size: 14px;
			color: var(--gray);
			margin-top: 24px;
		}
	</style>
</head>
<body>
	<main>
		<div class="success-container" role="status" aria-live="polite">
			<span class="checkmark" role="img" aria-label="Success checkmark">✓</span>
			<h1>Installation Complete!</h1>
			<p>Ready to Review is now supercharging</p>
			<h2>%s</h2>
			<p><span class="rocket" role="img" aria-label="Rocket">🚀</span>
			   Your dev team just got faster.
			   <span class="rocket" role="img" aria-label="Rocket">🚀</span></p>
			<p class="cta">Next:
			   <a href="https://github.com/codeGROOVE-dev/slacker/blob/main/docs/SETUP.md#configuring-your-repositories"
			      target="_blank"
			      rel="noopener noreferrer">Configure your repositories</a></p>
			<p class="footer">Then close this window and return to Slack.</p>
		</div>
	</main>
</body>
</html>
`, teamName); err != nil {
		slog.Error("failed to write success page", "error", err)
	}
}

// HandleInstall serves the "Add to Slack" installation page.
func (h *OAuthHandler) HandleInstall(writer http.ResponseWriter, _ *http.Request) {
	// SECURITY: Generate cryptographically secure random state token for CSRF protection
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		slog.Error("failed to generate OAuth state token", "error", err)
		http.Error(writer, "Internal server error", http.StatusInternalServerError)
		return
	}
	state := base64.URLEncoding.EncodeToString(stateBytes)

	// Store state in HttpOnly cookie with 10-minute expiration (OAuth should complete quickly)
	http.SetCookie(writer, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600, // 10 minutes
		HttpOnly: true,
		Secure:   true, // HTTPS only
		SameSite: http.SameSiteLaxMode,
	})

	// Build OAuth authorization URL
	scopes := []string{
		"app_mentions:read",
		"channels:history",
		"channels:read",
		"chat:write",
		"chat:write.public",
		"commands",
		"im:history",
		"im:read",
		"im:write",
		"reactions:write",
		"team:read",
		"users:read",
	}

	// Slack OAuth URL format with state parameter for CSRF protection
	// Use url.Values to ensure proper URL encoding of all parameters
	params := url.Values{
		"client_id": {h.clientID},
		"scope":     {strings.Join(scopes, ",")},
		"state":     {state},
	}
	authURL := "https://slack.com/oauth/v2/authorize?" + params.Encode()

	slog.Info("serving OAuth installation page",
		"client_id", h.clientID)

	h.writeInstallPage(writer, authURL)
}

// writeInstallPage renders the OAuth installation page.
func (*OAuthHandler) writeInstallPage(writer http.ResponseWriter, authURL string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(writer, `
<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Install Ready to Review</title>
	<link rel="preconnect" href="https://fonts.googleapis.com">
	<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
	<link href="https://fonts.googleapis.com/css2?family=Passion+One:wght@700&display=swap" rel="stylesheet">
	<style>
		:root {
			--yellow: #ffe838;
			--cyan: #5ce1e6;
			--black: #000000;
			--white: #ffffff;
		}
		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}
		body {
			font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
			background: var(--black);
			color: var(--white);
			min-height: 100vh;
			display: flex;
			align-items: center;
			justify-content: center;
			padding: 20px;
		}
		.install-container {
			max-width: 600px;
			background: var(--black);
			border: 8px solid var(--yellow);
			border-radius: 20px;
			padding: 50px 40px;
			text-align: center;
			box-shadow: 0 12px 40px rgba(255, 232, 56, 0.3);
		}
		h1 {
			font-family: "Passion One", cursive;
			font-size: 48px;
			font-weight: 700;
			font-style: italic;
			color: var(--white);
			margin-bottom: 16px;
			line-height: 1.2;
		}
		.tagline {
			font-size: 22px;
			font-weight: 700;
			color: var(--cyan);
			margin-bottom: 24px;
		}
		.features {
			list-style: none;
			margin: 24px 0;
			padding: 0;
		}
		.features li {
			font-size: 18px;
			color: var(--white);
			margin: 12px 0;
			line-height: 1.4;
		}
		.features li::before {
			content: "✓ ";
			color: var(--cyan);
			font-size: 20px;
			font-weight: 700;
		}
		.button-container {
			margin: 36px 0;
		}
		.add-to-slack {
			display: inline-block;
			transition: transform 0.2s, box-shadow 0.2s;
		}
		.add-to-slack:hover {
			transform: translateY(-2px);
		}
		.footer {
			font-size: 13px;
			color: #999;
			margin-top: 32px;
			line-height: 1.6;
		}
		.footer a {
			color: var(--cyan);
			text-decoration: none;
		}
		.footer a:hover {
			text-decoration: underline;
		}
	</style>
</head>
<body>
	<div class="install-container">
		<h1>READY TO REVIEW</h1>
		<p class="tagline">Supercharge your PR review workflow.</p>
		<ul class="features">
			<li>Real-time Slack notifications</li>
			<li>Smart DM reminders</li>
			<li>Multi-workspace support</li>
			<li>Auto-discovery channels</li>
		</ul>
		<div class="button-container">
			<a href="%s" class="add-to-slack">
				<img alt="Add to Slack" height="40" width="139"
				     src="https://platform.slack-edge.com/img/add_to_slack.png"
				     srcset="https://platform.slack-edge.com/img/add_to_slack.png 1x,
				             https://platform.slack-edge.com/img/add_to_slack@2x.png 2x" />
			</a>
		</div>
		<div class="footer">
			By installing, you agree to our
			<a href="https://github.com/codeGROOVE-dev/policy/blob/main/TOS.md">terms of service</a> and
			<a href="https://github.com/codeGROOVE-dev/policy/blob/main/PRIVACY.md">privacy policy</a>.
		</div>
	</div>
</body>
</html>
`, authURL); err != nil {
		slog.Error("failed to write installation page", "error", err)
	}
}

// OAuthResponse is a simplified response for debugging.
type OAuthResponse struct {
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
	BotUserID string `json:"bot_user_id"`
}

// HandleDebug returns JSON with installed workspaces (for debugging).
func (h *OAuthHandler) HandleDebug(writer http.ResponseWriter, _ *http.Request) {
	workspaces := h.manager.ListWorkspaces()

	response := make([]OAuthResponse, 0, len(workspaces))
	for _, ws := range workspaces {
		response = append(response, OAuthResponse{
			TeamID:    ws.TeamID,
			TeamName:  ws.TeamName,
			BotUserID: ws.BotUserID,
		})
	}

	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"workspaces": response,
		"count":      len(response),
	}); err != nil {
		slog.Error("failed to encode debug response", "error", err)
	}

	slog.Debug("served OAuth debug endpoint",
		"workspace_count", len(response))
}
