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

	// Parse the event to extract team_id
	// First, parse as a generic event to get the team_id
	var eventWrapper struct {
		TeamID string `json:"team_id"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(body, &eventWrapper); err != nil {
		slog.Error("failed to parse event wrapper", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Handle URL verification (doesn't need team-specific client)
	if eventWrapper.Type == "url_verification" {
		var challenge slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &challenge); err != nil {
			slog.Error("failed to unmarshal challenge", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge.Challenge))
		slog.Info("responded to URL verification challenge")
		return
	}

	teamID := eventWrapper.TeamID
	if teamID == "" {
		slog.Error("event missing team_id", "event_type", eventWrapper.Type)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	slog.Debug("routing event to workspace",
		"team_id", teamID,
		"event_type", eventWrapper.Type)

	// Get the workspace-specific client
	client, err := er.manager.GetClient(r.Context(), teamID)
	if err != nil {
		slog.Error("failed to get client for workspace",
			"team_id", teamID,
			"error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Forward to the workspace-specific client's event handler
	// We need to reconstruct the request with the original body
	r.Body = io.NopCloser(&readerWrapper{data: body})
	client.EventsHandler(w, r)
}

// HandleInteractions routes Slack interactions to the appropriate workspace client.
func (er *EventRouter) HandleInteractions(w http.ResponseWriter, r *http.Request) {
	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Parse payload to extract team_id
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

	slog.Debug("routing interaction to workspace", "team_id", teamID)

	// Get the workspace-specific client
	client, err := er.manager.GetClient(r.Context(), teamID)
	if err != nil {
		slog.Error("failed to get client for workspace",
			"team_id", teamID,
			"error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Forward to the workspace-specific client's interaction handler
	r.Body = io.NopCloser(&readerWrapper{data: body})
	client.InteractionsHandler(w, r)
}

// HandleSlashCommand routes slash commands to the appropriate workspace client.
func (er *EventRouter) HandleSlashCommand(w http.ResponseWriter, r *http.Request) {
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

	slog.Debug("routing slash command to workspace",
		"team_id", teamID,
		"command", r.FormValue("command"))

	// Get the workspace-specific client
	client, err := er.manager.GetClient(r.Context(), teamID)
	if err != nil {
		slog.Error("failed to get client for workspace",
			"team_id", teamID,
			"error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Forward to the workspace-specific client's slash command handler
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
