package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/slack-go/slack"
)

func TestNew(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		workspaceInfo: &slack.TeamInfo{
			ID:     "T123456",
			Name:   "Test Workspace",
			Domain: "testworkspace",
		},
	}

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	configMgr := NewMockConfig().Build()
	stateStore := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	coordinator := New(
		ctx,
		mockSlack,
		mockGH,
		configMgr,
		nil, // notifier not needed for this test
		"wss://test.example.com",
		stateStore,
	)

	if coordinator == nil {
		t.Fatal("expected non-nil coordinator")
	}

	if coordinator.slack != mockSlack {
		t.Error("slack client not set correctly")
	}

	if coordinator.github != mockGH {
		t.Error("github client not set correctly")
	}

	if coordinator.configManager != configMgr {
		t.Error("config manager not set correctly")
	}

	if coordinator.sprinklerURL != "wss://test.example.com" {
		t.Errorf("expected sprinklerURL 'wss://test.example.com', got %s", coordinator.sprinklerURL)
	}

	if coordinator.stateStore != stateStore {
		t.Error("state store not set correctly")
	}

	if coordinator.workspaceName != "testworkspace.slack.com" {
		t.Errorf("expected workspace name 'testworkspace.slack.com', got %s", coordinator.workspaceName)
	}

	if coordinator.threadCache == nil {
		t.Error("thread cache not initialized")
	}

	if coordinator.eventSemaphore == nil {
		t.Error("event semaphore not initialized")
	}

	if cap(coordinator.eventSemaphore) != 10 {
		t.Errorf("expected event semaphore capacity 10, got %d", cap(coordinator.eventSemaphore))
	}

	if coordinator.userMapper == nil {
		t.Error("user mapper not initialized")
	}
}

func TestNew_WorkspaceInfoFailure(t *testing.T) {
	ctx := context.Background()

	mockSlack := &mockSlackClient{
		workspaceInfo:    nil, // Will cause error
		workspaceInfoErr: true,
	}

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	configMgr := NewMockConfig().Build()
	stateStore := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	coordinator := New(
		ctx,
		mockSlack,
		mockGH,
		configMgr,
		nil, // notifier not needed for this test
		"wss://test.example.com",
		stateStore,
	)

	// Should still create coordinator even if workspace info fails
	if coordinator == nil {
		t.Fatal("expected non-nil coordinator")
	}

	// Workspace name should be empty when workspace info fails
	if coordinator.workspaceName != "" {
		t.Errorf("expected empty workspace name on error, got %s", coordinator.workspaceName)
	}
}

func TestNew_WithGitHubClient(t *testing.T) {
	ctx := context.Background()

	fakeGHClient := struct{}{} // Fake github client

	mockSlack := &mockSlackClient{
		workspaceInfo: &slack.TeamInfo{
			ID:     "T123456",
			Name:   "Test Workspace",
			Domain: "testworkspace",
		},
	}

	mockGH := &mockGitHub{
		org:    "testorg",
		token:  "test-token",
		client: fakeGHClient,
	}

	configMgr := NewMockConfig().Build()
	stateStore := &mockStateStore{
		processedEvents: make(map[string]bool),
	}

	coordinator := New(
		ctx,
		mockSlack,
		mockGH,
		configMgr,
		nil,
		"wss://test.example.com",
		stateStore,
	)

	if coordinator == nil {
		t.Fatal("expected non-nil coordinator")
	}

	// The GitHub client should have been set in the config manager
	// This tests the if ghClient != nil branch
}

func TestSaveThread(t *testing.T) {
	ctx := context.Background()
	mockSlack := &mockSlackClient{}
	configMgr := NewMockConfig().Build()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
		threads:         make(map[string]cache.ThreadInfo),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     mockState,
		configManager:  configMgr,
		notifier:       nil, // notifier not needed for this test
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	threadInfo := cache.ThreadInfo{
		ChannelID: "C123456",
		ThreadTS:  "1234567890.123456",
	}

	c.saveThread(ctx, "testorg", "testrepo", 42, "C123456", threadInfo)

	// Check cache
	key := "testorg/testrepo#42:C123456"
	cached, found := c.threadCache.Get(key)
	if !found {
		t.Error("expected thread to be in cache")
	}

	if cached.ChannelID != threadInfo.ChannelID {
		t.Errorf("expected channel ID %s, got %s", threadInfo.ChannelID, cached.ChannelID)
	}

	if cached.ThreadTS != threadInfo.ThreadTS {
		t.Errorf("expected thread TS %s, got %s", threadInfo.ThreadTS, cached.ThreadTS)
	}

	// Check persistent storage
	persistedKey := "thread:testorg/testrepo#42:C123456"
	persistedInfo, ok := mockState.threads[persistedKey]
	if !ok {
		t.Error("expected thread to be in persistent storage")
	}

	if persistedInfo.ChannelID != threadInfo.ChannelID {
		t.Errorf("expected persisted channel ID %s, got %s", threadInfo.ChannelID, persistedInfo.ChannelID)
	}
}

func TestSaveThread_PersistenceError(t *testing.T) {
	ctx := context.Background()
	mockSlack := &mockSlackClient{}
	configMgr := NewMockConfig().Build()

	mockState := &mockStateStore{
		processedEvents: make(map[string]bool),
		threads:         make(map[string]cache.ThreadInfo),
		saveThreadErr:   errors.New("database error"),
	}

	c := &Coordinator{
		github:         &mockGitHub{org: "testorg", token: "test-token"},
		slack:          mockSlack,
		stateStore:     mockState,
		configManager:  configMgr,
		notifier:       nil,
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	threadInfo := cache.ThreadInfo{
		ChannelID: "C123456",
		ThreadTS:  "1234567890.123456",
	}

	// Should still save to cache even if persistence fails
	c.saveThread(ctx, "testorg", "testrepo", 42, "C123456", threadInfo)

	// Check cache (should succeed)
	key := "testorg/testrepo#42:C123456"
	cached, found := c.threadCache.Get(key)
	if !found {
		t.Error("expected thread to be in cache even when persistence fails")
	}

	if cached.ChannelID != threadInfo.ChannelID {
		t.Errorf("expected channel ID %s, got %s", threadInfo.ChannelID, cached.ChannelID)
	}

	// Check persistent storage (should fail - nothing saved)
	persistedKey := "thread:testorg/testrepo#42:C123456"
	if _, ok := mockState.threads[persistedKey]; ok {
		t.Error("expected thread NOT to be in persistent storage after error")
	}
}

func TestThreadCache_Set(t *testing.T) {
	threadCache := cache.New()

	threadInfo := cache.ThreadInfo{
		ChannelID:   "C123456",
		ThreadTS:    "1234567890.123456",
		MessageText: "Test message",
		LastState:   "awaiting_review",
	}

	threadCache.Set("testorg/testrepo#42", threadInfo)

	retrieved, found := threadCache.Get("testorg/testrepo#42")
	if !found {
		t.Error("expected to find thread in cache")
	}

	if retrieved.ChannelID != threadInfo.ChannelID {
		t.Errorf("expected channel ID %s, got %s", threadInfo.ChannelID, retrieved.ChannelID)
	}
}
