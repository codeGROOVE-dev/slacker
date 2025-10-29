package slack

import (
	"context"

	"github.com/slack-go/slack"
)

// RegisterWorkspace registers a pre-configured Slack client for a workspace.
// This is only for integration testing - in production, clients are fetched from GSM.
func (m *Manager) RegisterWorkspace(ctx context.Context, teamID string, slackClient *slack.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()

	client := &Client{
		api:        slackClient,
		teamID:     teamID,
		stateStore: m.stateStore,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	m.clients[teamID] = client
	m.metadata[teamID] = &WorkspaceMetadata{
		TeamID:   teamID,
		TeamName: "test-workspace",
	}
}
