package slack

import (
	"context"
	"testing"
)

// TestManagerSetStateStore tests Manager.SetStateStore.
func TestManagerSetStateStore(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-signing-secret")
	mockStore := &mockStateStore{}

	// Create a client and add to manager's cache
	client := New("test-token", "test-secret")
	client.SetTeamID("T123")
	manager.clients["T123"] = client

	// Set state store on manager
	manager.SetStateStore(mockStore)

	// Verify state store was set on manager
	if manager.stateStore != mockStore {
		t.Error("expected state store to be set on manager")
	}

	// Verify state store was propagated to existing clients
	if client.stateStore != mockStore {
		t.Error("expected state store to be set on existing client")
	}
}

// TestManagerSetHomeViewHandler tests Manager.SetHomeViewHandler.
func TestManagerSetHomeViewHandler(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-signing-secret")

	// Create a client and add to manager's cache
	client := New("test-token", "test-secret")
	client.SetTeamID("T123")
	manager.clients["T123"] = client

	// Create a test handler
	handlerCalled := false
	handler := func(ctx context.Context, teamID, userID string) error {
		handlerCalled = true
		return nil
	}

	// Set handler on manager
	manager.SetHomeViewHandler(handler)

	// Verify handler was set on manager
	if manager.homeViewHandler == nil {
		t.Error("expected home view handler to be set on manager")
	}

	// Verify handler was propagated to existing clients
	if client.homeViewHandler == nil {
		t.Error("expected home view handler to be set on existing client")
	}

	// Verify handler works
	_ = client.homeViewHandler(context.Background(), "T123", "U123")
	if !handlerCalled {
		t.Error("expected handler to be called")
	}
}

// TestManagerInvalidateCache tests Manager.InvalidateCache.
func TestManagerInvalidateCache(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-signing-secret")

	// Create a client and metadata and add to manager's cache
	client := New("test-token", "test-secret")
	client.SetTeamID("T123")
	manager.clients["T123"] = client
	manager.metadata["T123"] = &WorkspaceMetadata{
		TeamID:   "T123",
		TeamName: "Test Team",
	}

	// Verify cache is populated
	if len(manager.clients) != 1 {
		t.Fatalf("expected 1 client in cache, got %d", len(manager.clients))
	}
	if len(manager.metadata) != 1 {
		t.Fatalf("expected 1 metadata entry in cache, got %d", len(manager.metadata))
	}

	// Invalidate cache
	manager.InvalidateCache("T123")

	// Verify cache is cleared
	if len(manager.clients) != 0 {
		t.Errorf("expected clients cache to be empty, got %d entries", len(manager.clients))
	}
	if len(manager.metadata) != 0 {
		t.Errorf("expected metadata cache to be empty, got %d entries", len(manager.metadata))
	}
}

// TestManagerListWorkspaces tests Manager.ListWorkspaces.
func TestManagerListWorkspaces(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-signing-secret")

	// Add some metadata to cache
	manager.metadata["T123"] = &WorkspaceMetadata{
		TeamID:   "T123",
		TeamName: "Team One",
	}
	manager.metadata["T456"] = &WorkspaceMetadata{
		TeamID:   "T456",
		TeamName: "Team Two",
	}

	// Get list of workspaces
	workspaces := manager.ListWorkspaces()

	// Verify count
	if len(workspaces) != 2 {
		t.Errorf("expected 2 workspaces, got %d", len(workspaces))
	}

	// Verify workspaces are in the list (order not guaranteed)
	found := make(map[string]bool)
	for _, ws := range workspaces {
		found[ws.TeamID] = true
	}

	if !found["T123"] {
		t.Error("expected T123 to be in workspaces list")
	}
	if !found["T456"] {
		t.Error("expected T456 to be in workspaces list")
	}
}

// TestManagerListWorkspacesEmpty tests Manager.ListWorkspaces with no cached workspaces.
func TestManagerListWorkspacesEmpty(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-signing-secret")

	workspaces := manager.ListWorkspaces()

	if len(workspaces) != 0 {
		t.Errorf("expected empty workspaces list, got %d entries", len(workspaces))
	}
}
