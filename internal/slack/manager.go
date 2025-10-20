package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/codeGROOVE-dev/gsm"
	"github.com/codeGROOVE-dev/slacker/internal/state"
)

// WorkspaceMetadata contains metadata about a Slack workspace installation.
type WorkspaceMetadata struct {
	TeamID      string `json:"team_id"`
	TeamName    string `json:"team_name"`
	BotUserID   string `json:"bot_user_id"`
	AccessToken string `json:"-"` // Never serialize token to JSON
}

// StateStore interface for DM message tracking.
type StateStore interface {
	DMMessage(userID, prURL string) (state.DMInfo, bool)
	SaveDMMessage(userID, prURL string, info state.DMInfo) error
}

// Manager manages Slack clients for multiple workspaces.
type Manager struct {
	clients         map[string]*Client // team_id -> client
	metadata        map[string]*WorkspaceMetadata
	signingSecret   string
	homeViewHandler func(ctx context.Context, teamID, userID string) error // Global home view handler
	stateStore      StateStore                                             // State store for DM message tracking
	mu              sync.RWMutex
}

// NewManager creates a new Slack client manager.
func NewManager(signingSecret string) *Manager {
	return &Manager{
		clients:       make(map[string]*Client),
		metadata:      make(map[string]*WorkspaceMetadata),
		signingSecret: signingSecret,
	}
}

// SetStateStore sets the state store for DM message tracking.
func (m *Manager) SetStateStore(store StateStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateStore = store

	// Update existing clients with the state store
	for _, client := range m.clients {
		client.SetStateStore(store)
	}
}

// Client returns a Slack client for the given workspace.
// If the client doesn't exist in cache, it fetches the token from GSM.
func (m *Manager) Client(ctx context.Context, teamID string) (*Client, error) {
	// Check cache first
	m.mu.RLock()
	client, exists := m.clients[teamID]
	m.mu.RUnlock()

	if exists {
		slog.Debug("using cached Slack client",
			"team_id", teamID)
		return client, nil
	}

	// Fetch token from GSM
	slog.Info("fetching Slack token from GSM for workspace",
		"team_id", teamID,
		"secret_name", fmt.Sprintf("slack-token-%s", teamID))

	token, err := gsm.Fetch(ctx, fmt.Sprintf("slack-token-%s", teamID))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token for workspace %s: %w", teamID, err)
	}

	// Fetch metadata
	metadataJSON, err := gsm.Fetch(ctx, fmt.Sprintf("slack-metadata-%s", teamID))
	if err != nil {
		slog.Warn("failed to fetch metadata for workspace, will use defaults",
			"team_id", teamID,
			"error", err)
		metadataJSON = fmt.Sprintf(`{"team_id":%q,"team_name":"unknown"}`, teamID)
	}

	var metadata WorkspaceMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		slog.Error("failed to parse metadata for workspace",
			"team_id", teamID,
			"error", err)
		metadata = WorkspaceMetadata{
			TeamID:   teamID,
			TeamName: "unknown",
		}
	}

	// Create client
	client = New(token, m.signingSecret)
	client.SetTeamID(teamID)
	client.SetManager(m) // Set manager reference for cache invalidation

	// Set home view handler if configured
	if m.homeViewHandler != nil {
		client.SetHomeViewHandler(m.homeViewHandler)
	}

	// Set state store if configured
	if m.stateStore != nil {
		client.SetStateStore(m.stateStore)
	}

	// Cache it
	m.mu.Lock()
	m.clients[teamID] = client
	m.metadata[teamID] = &metadata
	m.mu.Unlock()

	slog.Info("created and cached Slack client for workspace",
		"team_id", teamID,
		"team_name", metadata.TeamName,
		"bot_user_id", metadata.BotUserID)

	return client, nil
}

// SetHomeViewHandler sets the home view handler on all current and future clients.
func (m *Manager) SetHomeViewHandler(handler func(ctx context.Context, teamID, userID string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store for future clients
	m.homeViewHandler = handler

	// Set on all existing clients
	for _, client := range m.clients {
		client.SetHomeViewHandler(handler)
	}
}

// StoreWorkspace stores a workspace's token and metadata in GSM.
func (m *Manager) StoreWorkspace(ctx context.Context, metadata *WorkspaceMetadata, token string) error {
	slog.Info("storing workspace token and metadata in GSM",
		"team_id", metadata.TeamID,
		"team_name", metadata.TeamName,
		"bot_user_id", metadata.BotUserID)

	// Store token
	if err := gsm.Store(ctx, fmt.Sprintf("slack-token-%s", metadata.TeamID), token); err != nil {
		return fmt.Errorf("failed to store token: %w", err)
	}

	// Store metadata
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := gsm.Store(ctx, fmt.Sprintf("slack-metadata-%s", metadata.TeamID), string(metadataJSON)); err != nil {
		return fmt.Errorf("failed to store metadata: %w", err)
	}

	// Invalidate cache to force reload
	m.mu.Lock()
	delete(m.clients, metadata.TeamID)
	delete(m.metadata, metadata.TeamID)
	m.mu.Unlock()

	slog.Info("successfully stored workspace credentials",
		"team_id", metadata.TeamID,
		"team_name", metadata.TeamName)

	return nil
}

// InvalidateCache removes a workspace from the cache, forcing a reload on next access.
func (m *Manager) InvalidateCache(teamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.clients, teamID)
	delete(m.metadata, teamID)

	slog.Info("invalidated Slack client cache for workspace",
		"team_id", teamID)
}

// ListWorkspaces returns metadata for all cached workspaces.
func (m *Manager) ListWorkspaces() []*WorkspaceMetadata {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*WorkspaceMetadata, 0, len(m.metadata))
	for _, meta := range m.metadata {
		result = append(result, meta)
	}
	return result
}
