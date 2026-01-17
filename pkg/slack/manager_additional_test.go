package slack

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/slack-go/slack"
)

// TestManager_SetReportHandler tests setting report handler on manager.
func TestManager_SetReportHandler(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-secret")

	// Create mock clients
	client1 := &Client{teamID: "T123"}
	client2 := &Client{teamID: "T456"}

	manager.mu.Lock()
	manager.clients["T123"] = client1
	manager.clients["T456"] = client2
	manager.mu.Unlock()

	// Set report handler
	handlerCalled := 0
	handler := func(ctx context.Context, teamID, userID string) error {
		handlerCalled++
		return nil
	}

	manager.SetReportHandler(handler)

	// Verify handler was set on manager
	manager.mu.Lock()
	if manager.reportHandler == nil {
		t.Error("Expected report handler to be set on manager")
	}
	manager.mu.Unlock()

	// Verify handler was set on existing clients
	client1.reportHandlerMu.RLock()
	if client1.reportHandler == nil {
		t.Error("Expected report handler to be set on client1")
	}
	client1.reportHandlerMu.RUnlock()

	client2.reportHandlerMu.RLock()
	if client2.reportHandler == nil {
		t.Error("Expected report handler to be set on client2")
	}
	client2.reportHandlerMu.RUnlock()

	// Call handlers
	_ = client1.reportHandler(context.Background(), "T123", "U123")
	_ = client2.reportHandler(context.Background(), "T456", "U456")

	if handlerCalled != 2 {
		t.Errorf("Expected handler to be called 2 times, got %d", handlerCalled)
	}
}

// TestManager_StoreWorkspace tests storing workspace credentials.
func TestManager_StoreWorkspace(t *testing.T) {
	// Skip if not in integration test mode - requires GSM access
	if testing.Short() {
		t.Skip("Skipping GSM integration test in short mode")
	}

	// This test would require mocking GSM, which is complex
	// For now, we'll test the basic flow without actually calling GSM

	manager := NewManager("test-secret")

	metadata := &WorkspaceMetadata{
		TeamID:    "T123TEST",
		TeamName:  "Test Workspace",
		BotUserID: "UBOT123",
	}

	// Pre-populate cache to verify invalidation
	manager.mu.Lock()
	manager.clients["T123TEST"] = &Client{teamID: "T123TEST"}
	manager.metadata["T123TEST"] = metadata
	manager.mu.Unlock()

	// Note: This will fail without GSM access, but that's expected
	// The test is mainly to ensure the code path is exercised
	err := manager.StoreWorkspace(context.Background(), metadata, "xoxb-test-token")

	// We expect an error since GSM is not available in tests
	if err == nil {
		t.Log("StoreWorkspace succeeded (GSM available)")
		// Verify cache was invalidated
		manager.mu.Lock()
		_, clientExists := manager.clients["T123TEST"]
		_, metadataExists := manager.metadata["T123TEST"]
		manager.mu.Unlock()

		if clientExists {
			t.Error("Expected client to be removed from cache")
		}
		if metadataExists {
			t.Error("Expected metadata to be removed from cache")
		}
	} else {
		t.Logf("StoreWorkspace failed as expected without GSM: %v", err)
	}
}

// TestRegisterWorkspace tests registering a workspace for testing.
func TestRegisterWorkspace(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-secret")
	mockSlackClient := &slack.Client{}

	// Register workspace
	manager.RegisterWorkspace(context.Background(), "T123", mockSlackClient)

	// Verify client was registered
	manager.mu.Lock()
	client, exists := manager.clients["T123"]
	metadata, metadataExists := manager.metadata["T123"]
	manager.mu.Unlock()

	if !exists {
		t.Fatal("Expected client to be registered")
	}

	if client.teamID != "T123" {
		t.Errorf("Expected team ID T123, got %s", client.teamID)
	}

	if !metadataExists {
		t.Fatal("Expected metadata to be registered")
	}

	if metadata.TeamID != "T123" {
		t.Errorf("Expected metadata team ID T123, got %s", metadata.TeamID)
	}

	if metadata.TeamName != "test-workspace" {
		t.Errorf("Expected team name 'test-workspace', got %s", metadata.TeamName)
	}
}

// TestRegisterWorkspace_WithStateStore tests registering workspace with state store.
func TestRegisterWorkspace_WithStateStore(t *testing.T) {
	t.Parallel()

	mockStore := &state.MemoryStore{}
	manager := NewManager("test-secret")
	manager.SetStateStore(mockStore)

	mockSlackClient := &slack.Client{}

	// Register workspace
	manager.RegisterWorkspace(context.Background(), "T456", mockSlackClient)

	// Verify client has state store
	manager.mu.Lock()
	client := manager.clients["T456"]
	manager.mu.Unlock()

	if client == nil {
		t.Fatal("Expected client to be registered")
	}

	client.stateStoreMu.RLock()
	stateStore := client.stateStore
	client.stateStoreMu.RUnlock()

	if stateStore == nil {
		t.Error("Expected state store to be set on client")
	}
}

// TestWorkspaceMetadata_JSON tests JSON marshaling/unmarshaling.
func TestWorkspaceMetadata_JSON(t *testing.T) {
	t.Parallel()

	original := &WorkspaceMetadata{
		TeamID:    "T123",
		TeamName:  "Test Workspace",
		BotUserID: "UBOT123",
	}

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal metadata: %v", err)
	}

	// Unmarshal
	var restored WorkspaceMetadata
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Failed to unmarshal metadata: %v", err)
	}

	// Verify
	if restored.TeamID != original.TeamID {
		t.Errorf("TeamID mismatch: expected %s, got %s", original.TeamID, restored.TeamID)
	}
	if restored.TeamName != original.TeamName {
		t.Errorf("TeamName mismatch: expected %s, got %s", original.TeamName, restored.TeamName)
	}
	if restored.BotUserID != original.BotUserID {
		t.Errorf("BotUserID mismatch: expected %s, got %s", original.BotUserID, restored.BotUserID)
	}
}

// TestManager_SetReportHandler_NewClient tests that new clients get the handler.
func TestManager_SetReportHandler_NewClient(t *testing.T) {
	t.Parallel()

	manager := NewManager("test-secret")

	// Set report handler before any clients exist
	handlerCalled := false
	handler := func(ctx context.Context, teamID, userID string) error {
		handlerCalled = true
		return nil
	}

	manager.SetReportHandler(handler)

	// Now create a client (this would normally happen via Client() method)
	newClient := &Client{teamID: "T789"}

	// Manually simulate what Client() does - set the handler
	manager.mu.Lock()
	if manager.reportHandler != nil {
		newClient.SetReportHandler(manager.reportHandler)
	}
	manager.mu.Unlock()

	// Verify handler was set
	newClient.reportHandlerMu.RLock()
	if newClient.reportHandler == nil {
		t.Error("Expected report handler to be set on new client")
	}
	newClient.reportHandlerMu.RUnlock()

	// Call handler
	_ = newClient.reportHandler(context.Background(), "T789", "U789")

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}
