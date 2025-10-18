// Package slack provides event routing for multi-workspace support.
package slack

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/slack-go/slack/slackevents"
)

// EventRouter routes Slack events to workspace-specific clients.
type EventRouter struct {
	manager *Manager
}

// NewEventRouter creates a new event router.
func NewEventRouter(manager *Manager) *EventRouter {
	return &EventRouter{
		manager: manager,
	}
}

// HandleEvents routes incoming Slack events to the appropriate workspace client.
func (er *EventRouter) HandleEvents(w http.ResponseWriter, r *http.Request) {
	// Read body for verification and parsing
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Parse the event to extract team_id FIRST (before signature verification)
	// We need team_id to get the correct signing secret
	var eventWrapper struct {
		TeamID string `json:"team_id"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(body, &eventWrapper); err != nil {
		slog.Error("failed to parse event wrapper", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Handle URL verification (doesn't need signature verification or team-specific client)
	// URL verification happens during initial Slack app setup before we have the signing secret
	if eventWrapper.Type == "url_verification" {
		var challenge slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &challenge); err != nil {
			slog.Error("failed to unmarshal challenge", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(challenge.Challenge)); err != nil {
			slog.Error("failed to write challenge response", "error", err)
		}
		slog.Info("responded to URL verification challenge")
		return
	}

	teamID := eventWrapper.TeamID
	if teamID == "" {
		slog.Error("event missing team_id", "event_type", eventWrapper.Type)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Get the workspace-specific client to access its signing secret for verification
	client, err := er.manager.Client(r.Context(), teamID)
	if err != nil {
		slog.Error("failed to get client for workspace",
			"team_id", teamID,
			"error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// CRITICAL: Verify Slack signature BEFORE processing the event
	// This prevents attackers from forging events claiming to be from arbitrary workspaces
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	if !client.VerifySignature(signature, timestamp, body) {
		slog.Warn("webhook signature verification failed - possible attack",
			"team_id", teamID,
			"event_type", eventWrapper.Type,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	slog.Debug("routing event to workspace",
		"team_id", teamID,
		"event_type", eventWrapper.Type)

	// Forward to the workspace-specific client's event handler
	// We need to reconstruct the request with the original body
	r.Body = io.NopCloser(&readerWrapper{data: body})
	client.EventsHandler(w, r)
}

// HandleInteractions routes Slack interactions to the appropriate workspace client.
func (er *EventRouter) HandleInteractions(w http.ResponseWriter, r *http.Request) {
	// Read body for signature verification
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Parse payload to extract team_id FIRST (before signature verification)
	// Interactions come as form-encoded with a "payload" field
	payload := r.FormValue("payload")
	if payload == "" {
		// Try reading from body
		payload = string(body)
	}

	var interaction struct {
		Team struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	if err := json.Unmarshal([]byte(payload), &interaction); err != nil {
		slog.Error("failed to parse interaction payload", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	teamID := interaction.Team.ID
	if teamID == "" {
		slog.Error("interaction missing team.id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Get the workspace-specific client to access its signing secret for verification
	client, err := er.manager.Client(r.Context(), teamID)
	if err != nil {
		slog.Error("failed to get client for workspace",
			"team_id", teamID,
			"error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// CRITICAL: Verify Slack signature BEFORE processing the interaction
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	if !client.VerifySignature(signature, timestamp, body) {
		slog.Warn("interaction signature verification failed - possible attack",
			"team_id", teamID,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	slog.Debug("routing interaction to workspace", "team_id", teamID)

	// Forward to the workspace-specific client's interaction handler
	r.Body = io.NopCloser(&readerWrapper{data: body})
	client.InteractionsHandler(w, r)
}

// HandleSlashCommand routes slash commands to the appropriate workspace client.
func (er *EventRouter) HandleSlashCommand(w http.ResponseWriter, r *http.Request) {
	// Read body for signature verification BEFORE parsing form
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Restore body for form parsing
	r.Body = io.NopCloser(&readerWrapper{data: body})

	// Parse form to get team_id
	if err := r.ParseForm(); err != nil {
		slog.Error("failed to parse form", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	teamID := r.FormValue("team_id")
	if teamID == "" {
		slog.Error("slash command missing team_id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Get the workspace-specific client to access its signing secret for verification
	client, err := er.manager.Client(r.Context(), teamID)
	if err != nil {
		slog.Error("failed to get client for workspace",
			"team_id", teamID,
			"error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// CRITICAL: Verify Slack signature BEFORE processing the command
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	if !client.VerifySignature(signature, timestamp, body) {
		slog.Warn("slash command signature verification failed - possible attack",
			"team_id", teamID,
			"command", r.FormValue("command"),
			"remote_addr", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	slog.Debug("routing slash command to workspace",
		"team_id", teamID,
		"command", r.FormValue("command"))

	// Forward to the workspace-specific client's slash command handler
	// Restore body again since it was consumed by signature verification
	r.Body = io.NopCloser(&readerWrapper{data: body})
	client.SlashCommandHandler(w, r)
}

// readerWrapper wraps a byte slice as an io.Reader.
type readerWrapper struct {
	data []byte
}

func (rw *readerWrapper) Read(p []byte) (n int, err error) {
	if len(rw.data) == 0 {
		return 0, io.EOF
	}
	n = copy(p, rw.data)
	rw.data = rw.data[n:]
	return n, nil
}
