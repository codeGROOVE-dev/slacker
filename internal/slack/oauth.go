// Package slack provides OAuth handling for multi-workspace installation.
package slack

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/slack-go/slack"
)

// OAuthHandler handles the OAuth callback from Slack.
type OAuthHandler struct {
	manager      *Manager
	clientID     string
	clientSecret string
}

// NewOAuthHandler creates a new OAuth handler.
func NewOAuthHandler(manager *Manager, clientID, clientSecret string) *OAuthHandler {
	return &OAuthHandler{
		manager:      manager,
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

// HandleCallback handles the OAuth callback from Slack.
func (h *OAuthHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract authorization code
	code := r.URL.Query().Get("code")
	if code == "" {
		slog.Error("OAuth callback missing code parameter")
		http.Error(w, "Missing code parameter", http.StatusBadRequest)
		return
	}

	errorParam := r.URL.Query().Get("error")
	if errorParam != "" {
		slog.Error("OAuth callback received error",
			"error", errorParam)
		http.Error(w, fmt.Sprintf("OAuth error: %s", errorParam), http.StatusBadRequest)
		return
	}

	slog.Info("received OAuth callback",
		"code_prefix", code[:min(len(code), 10)]+"...")

	// Exchange code for token with retry
	var resp *slack.OAuthV2Response
	err := retry.Do(
		func() error {
			var err error
			resp, err = slack.GetOAuthV2ResponseContext(
				ctx,
				&http.Client{},
				h.clientID,
				h.clientSecret,
				code,
				"", // redirect URI - leave empty if not specified during authorization
			)
			if err != nil {
				slog.Warn("failed to exchange OAuth code for token, will retry",
					"error", err)
				return err
			}
			return nil
		},
		retry.Attempts(3),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.MaxJitter(time.Second),
		retry.LastErrorOnly(true),
		retry.Context(ctx),
	)
	if err != nil {
		slog.Error("failed to exchange OAuth code for token",
			"error", err)
		http.Error(w, "Failed to complete OAuth flow", http.StatusInternalServerError)
		return
	}

	if !resp.Ok {
		slog.Error("OAuth token exchange returned not ok",
			"error", resp.Error)
		http.Error(w, fmt.Sprintf("OAuth error: %s", resp.Error), http.StatusInternalServerError)
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

	if err := h.manager.StoreWorkspace(ctx, metadata, botToken); err != nil {
		slog.Error("failed to store workspace credentials",
			"team_id", teamID,
			"team_name", teamName,
			"error", err)
		http.Error(w, "Failed to store credentials", http.StatusInternalServerError)
		return
	}

	// Return success page
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
	<title>Installation Complete</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
		       max-width: 600px; margin: 100px auto; text-align: center; }
		h1 { color: #2eb67d; }
		p { color: #1d1c1d; line-height: 1.6; }
	</style>
</head>
<body>
	<h1>✓ Installation Complete</h1>
	<p>Ready to Review has been successfully installed to <strong>%s</strong>.</p>
	<p>You can now close this window and return to Slack.</p>
</body>
</html>
`, teamName)

	slog.Info("OAuth installation completed successfully",
		"team_id", teamID,
		"team_name", teamName)
}

// HandleInstall serves the "Add to Slack" installation page.
func (h *OAuthHandler) HandleInstall(w http.ResponseWriter, r *http.Request) {
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

	// Slack OAuth URL format
	authURL := fmt.Sprintf(
		"https://slack.com/oauth/v2/authorize?client_id=%s&scope=%s",
		h.clientID,
		joinScopes(scopes),
	)

	slog.Info("serving OAuth installation page",
		"client_id", h.clientID)

	// Return installation page with "Add to Slack" button
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
	<title>Install Ready to Review</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
		       max-width: 600px; margin: 100px auto; text-align: center; }
		h1 { color: #1d1c1d; }
		p { color: #616061; line-height: 1.6; margin: 20px 0; }
		.button { display: inline-block; margin: 30px 0; }
	</style>
</head>
<body>
	<h1>Ready to Review</h1>
	<p>Streamline your PR review workflow with real-time notifications and dashboard views.</p>
	<div class="button">
		<a href="%s">
			<img alt="Add to Slack" height="40" width="139"
			     src="https://platform.slack-edge.com/img/add_to_slack.png"
			     srcset="https://platform.slack-edge.com/img/add_to_slack.png 1x,
			             https://platform.slack-edge.com/img/add_to_slack@2x.png 2x" />
		</a>
	</div>
	<p><small>By installing, you agree to our <a href="https://github.com/codeGROOVE-dev/policy/blob/main/TOS.md">terms of service</a> and <a href="https://github.com/codeGROOVE-dev/policy/blob/main/PRIVACY.md">privacy policy</a>.</small></p>
</body>
</html>
`, authURL)
}

// joinScopes joins scope names with commas for OAuth URL.
func joinScopes(scopes []string) string {
	result := ""
	for i, scope := range scopes {
		if i > 0 {
			result += ","
		}
		result += scope
	}
	return result
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// OAuthResponse is a simplified response for debugging.
type OAuthResponse struct {
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
	BotUserID string `json:"bot_user_id"`
}

// HandleDebug returns JSON with installed workspaces (for debugging).
func (h *OAuthHandler) HandleDebug(w http.ResponseWriter, r *http.Request) {
	workspaces := h.manager.ListWorkspaces()

	response := make([]OAuthResponse, 0, len(workspaces))
	for _, ws := range workspaces {
		response = append(response, OAuthResponse{
			TeamID:    ws.TeamID,
			TeamName:  ws.TeamName,
			BotUserID: ws.BotUserID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"workspaces": response,
		"count":      len(response),
	})

	slog.Debug("served OAuth debug endpoint",
		"workspace_count", len(response))
}
