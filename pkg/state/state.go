// Package state manages application state with file persistence.
package state

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// NotificationState tracks when users were last notified.
type NotificationState struct {
	LastChannelNotification time.Time `json:"last_channel_notification"`
	LastDMNotification      time.Time `json:"last_dm_notification"`
	LastDailyReminder       time.Time `json:"last_daily_reminder"`
}

// PRState represents the current state of a PR.
type PRState struct {
	LastUpdated             time.Time         `json:"last_updated"`
	LastChannelNotification time.Time         `json:"last_channel_notification"`
	UserNotifications       map[string]string `json:"user_notifications"`
	State                   string            `json:"state"`
	Title                   string            `json:"title"`
	Author                  string            `json:"author"`
	Repo                    string            `json:"repo"`
	ThreadTS                string            `json:"thread_ts"`
	ChannelID               string            `json:"channel_id"`
	Owner                   string            `json:"owner"`
	BlockedOn               []string          `json:"blocked_on"`
	Reviewers               []string          `json:"reviewers"`
	Number                  int               `json:"number"`
}

// WorkspaceData holds data for a Slack workspace.
type WorkspaceData struct {
	LastUpdated       time.Time                    `json:"last_updated"`
	UserNotifications map[string]NotificationState `json:"user_notifications"`
	PRs               map[string]*PRState          `json:"prs"`
	UserPRs           map[string][]string          `json:"user_prs"`
	WorkspaceID       string                       `json:"workspace_id"`
}

// Manager manages application state with file persistence.
type Manager struct {
	data     map[string]*WorkspaceData
	saveChan chan string
	dataDir  string
	mu       sync.RWMutex
}

// New creates a new state manager.
func New(dataDir string) *Manager {
	m := &Manager{
		dataDir:  dataDir,
		data:     make(map[string]*WorkspaceData),
		saveChan: make(chan string, 100),
	}

	// Create data directory if it doesn't exist with restrictive permissions.
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		slog.Error("failed to create data directory", "error", err)
	}

	// Start background save worker.
	go m.saveWorker()

	return m
}

// GetNotificationState returns notification state for a user.
func (m *Manager) GetNotificationState(workspaceID, userID string) NotificationState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Load workspace data if not in memory.
	if _, exists := m.data[workspaceID]; !exists {
		slog.Debug("workspace not in memory, loading from disk",
			"workspace", workspaceID,
			"user", userID)
		m.mu.RUnlock()
		m.loadWorkspaceData(workspaceID)
		m.mu.RLock()
	}

	workspace, exists := m.data[workspaceID]
	if !exists || workspace.UserNotifications == nil {
		slog.Debug("returning empty notification state - no workspace data",
			"workspace", workspaceID,
			"user", userID,
			"workspace_exists", exists,
			"has_notifications", workspace != nil && workspace.UserNotifications != nil)
		return NotificationState{}
	}

	state, exists := workspace.UserNotifications[userID]
	if !exists {
		slog.Debug("returning empty notification state - user not found",
			"workspace", workspaceID,
			"user", userID,
			"total_users_in_workspace", len(workspace.UserNotifications))
		return NotificationState{}
	}

	slog.Debug("retrieved user notification state",
		"workspace", workspaceID,
		"user", userID,
		"last_dm", state.LastDMNotification,
		"last_daily", state.LastDailyReminder,
		"has_dm_history", !state.LastDMNotification.IsZero(),
		"has_daily_history", !state.LastDailyReminder.IsZero())

	return state
}

// SetNotificationState updates notification state for a user.
func (m *Manager) SetNotificationState(workspaceID, userID string, state NotificationState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workspace := m.ensureWorkspace(workspaceID)
	if workspace.UserNotifications == nil {
		workspace.UserNotifications = make(map[string]NotificationState)
	}
	workspace.UserNotifications[userID] = state
	workspace.LastUpdated = time.Now()

	// Queue save.
	select {
	case m.saveChan <- workspaceID:
	default:
		// Channel full, save will happen soon anyway.
	}
}

// PRState returns the state of a PR.
func (m *Manager) PRState(workspaceID, owner, repo string, number int) (*PRState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workspace, exists := m.data[workspaceID]
	if !exists || workspace.PRs == nil {
		return nil, false
	}

	key := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	pr, exists := workspace.PRs[key]
	return pr, exists
}

// SetPRState updates the state of a PR.
func (m *Manager) SetPRState(workspaceID string, pr *PRState) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workspace := m.ensureWorkspace(workspaceID)
	if workspace.PRs == nil {
		workspace.PRs = make(map[string]*PRState)
	}

	key := fmt.Sprintf("%s/%s#%d", pr.Owner, pr.Repo, pr.Number)
	workspace.PRs[key] = pr
	workspace.LastUpdated = time.Now()

	// Update user PR mappings.
	if workspace.UserPRs == nil {
		workspace.UserPRs = make(map[string][]string)
	}

	// Add to blocked users' lists.
	for _, userID := range pr.BlockedOn {
		// Check if PR key already exists in user's list
		alreadyExists := false
		for _, prKey := range workspace.UserPRs[userID] {
			if prKey == key {
				alreadyExists = true
				break
			}
		}
		if !alreadyExists {
			workspace.UserPRs[userID] = append(workspace.UserPRs[userID], key)
		}
	}

	// Queue save.
	select {
	case m.saveChan <- workspaceID:
	default:
	}
}

// UserPRs returns PRs associated with a user.
func (m *Manager) UserPRs(workspaceID, userID string) []*PRState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workspace, exists := m.data[workspaceID]
	if !exists || workspace.UserPRs == nil {
		return nil
	}

	prKeys, exists := workspace.UserPRs[userID]
	if !exists {
		return nil
	}

	var prs []*PRState
	for _, key := range prKeys {
		if pr, ok := workspace.PRs[key]; ok {
			prs = append(prs, pr)
		}
	}
	return prs
}

// UpdateDMNotification updates the last DM notification time for a user.
func (m *Manager) UpdateDMNotification(workspaceID, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workspace := m.ensureWorkspace(workspaceID)
	if workspace.UserNotifications == nil {
		workspace.UserNotifications = make(map[string]NotificationState)
	}

	previousState := workspace.UserNotifications[userID]
	state := workspace.UserNotifications[userID]
	state.LastDMNotification = time.Now()
	workspace.UserNotifications[userID] = state

	slog.Info("updated user DM notification timestamp",
		"workspace", workspaceID,
		"user", userID,
		"previous_dm_time", previousState.LastDMNotification,
		"new_dm_time", state.LastDMNotification,
		"last_daily_reminder", state.LastDailyReminder,
		"state_saved", true)

	// Queue save.
	select {
	case m.saveChan <- workspaceID:
		slog.Debug("queued workspace state save",
			"workspace", workspaceID,
			"trigger", "dm_notification_update")
	default:
		slog.Debug("save channel full - save will happen soon",
			"workspace", workspaceID,
			"trigger", "dm_notification_update")
	}
}

// UpdateDailyReminder updates the last daily reminder time for a user.
func (m *Manager) UpdateDailyReminder(workspaceID, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workspace := m.ensureWorkspace(workspaceID)
	if workspace.UserNotifications == nil {
		workspace.UserNotifications = make(map[string]NotificationState)
	}

	previousState := workspace.UserNotifications[userID]
	state := workspace.UserNotifications[userID]
	state.LastDailyReminder = time.Now()
	workspace.UserNotifications[userID] = state

	slog.Info("updated user daily reminder timestamp",
		"workspace", workspaceID,
		"user", userID,
		"previous_daily_time", previousState.LastDailyReminder,
		"new_daily_time", state.LastDailyReminder,
		"last_dm_notification", state.LastDMNotification,
		"state_saved", true)

	// Queue save.
	select {
	case m.saveChan <- workspaceID:
		slog.Debug("queued workspace state save",
			"workspace", workspaceID,
			"trigger", "daily_reminder_update")
	default:
		slog.Debug("save channel full - save will happen soon",
			"workspace", workspaceID,
			"trigger", "daily_reminder_update")
	}
}

// UpdateChannelNotification updates the last channel notification time for a PR.
func (m *Manager) UpdateChannelNotification(workspaceID, owner, repo string, number int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	workspace := m.ensureWorkspace(workspaceID)

	if workspace.PRs == nil {
		workspace.PRs = make(map[string]*PRState)
	}

	if pr, exists := workspace.PRs[key]; exists {
		previousTime := pr.LastChannelNotification
		pr.LastChannelNotification = time.Now()
		workspace.LastUpdated = time.Now()

		slog.Info("updated PR channel notification timestamp",
			"workspace", workspaceID,
			"pr", key,
			"previous_channel_time", previousTime,
			"new_channel_time", pr.LastChannelNotification,
			"pr_state", pr.State,
			"channel", pr.ChannelID,
			"thread_ts", pr.ThreadTS,
			"will_delay_dm_notifications", true)

		// Queue save.
		select {
		case m.saveChan <- workspaceID:
			slog.Debug("queued workspace state save",
				"workspace", workspaceID,
				"trigger", "channel_notification_update",
				"pr", key)
		default:
			slog.Debug("save channel full - save will happen soon",
				"workspace", workspaceID,
				"trigger", "channel_notification_update",
				"pr", key)
		}
	} else {
		slog.Warn("attempted to update channel notification for non-existent PR",
			"workspace", workspaceID,
			"pr", key,
			"pr_exists", false,
			"total_prs_in_workspace", len(workspace.PRs))
	}
}

// ensureWorkspace ensures a workspace exists in memory.
func (m *Manager) ensureWorkspace(workspaceID string) *WorkspaceData {
	if workspace, exists := m.data[workspaceID]; exists {
		return workspace
	}

	// Try to load from disk.
	if data := m.loadWorkspaceDataLocked(workspaceID); data != nil {
		m.data[workspaceID] = data
		return data
	}

	// Create new.
	workspace := &WorkspaceData{
		WorkspaceID:       workspaceID,
		UserNotifications: make(map[string]NotificationState),
		PRs:               make(map[string]*PRState),
		UserPRs:           make(map[string][]string),
		LastUpdated:       time.Now(),
	}
	m.data[workspaceID] = workspace
	return workspace
}

// loadWorkspaceData loads workspace data from disk.
func (m *Manager) loadWorkspaceData(workspaceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if data := m.loadWorkspaceDataLocked(workspaceID); data != nil {
		m.data[workspaceID] = data
	}
}

// loadWorkspaceDataLocked loads workspace data from disk (must hold lock).
func (m *Manager) loadWorkspaceDataLocked(workspaceID string) *WorkspaceData {
	// Sanitize workspaceID to prevent path traversal.
	safeID := filepath.Base(workspaceID)
	if safeID != workspaceID || safeID == "." || safeID == ".." {
		slog.Error("invalid workspace ID", "workspace_id", workspaceID)
		return nil
	}

	filename := filepath.Join(m.dataDir, fmt.Sprintf("%s.json.gz", safeID))

	file, err := os.Open(filename)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to open state file", "file", filename, "error", err)
		}
		return nil
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
		}
	}()

	gz, err := gzip.NewReader(file)
	if err != nil {
		slog.Error("failed to create gzip reader", "error", err)
		return nil
	}
	defer func() {
		if err := gz.Close(); err != nil {
			slog.Error("failed to close gzip reader", "error", err)
		}
	}()

	var data WorkspaceData
	if err := json.NewDecoder(gz).Decode(&data); err != nil {
		slog.Error("failed to decode state data", "error", err)
		return nil
	}

	slog.Info("loaded state", "workspace", workspaceID, "notifications", len(data.UserNotifications), "prs", len(data.PRs))
	return &data
}

// saveWorker handles background saves.
func (m *Manager) saveWorker() {
	saved := make(map[string]time.Time)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case workspaceID := <-m.saveChan:
			// Debounce saves - wait at least 5 seconds between saves.
			if lastSave, exists := saved[workspaceID]; exists && time.Since(lastSave) < 5*time.Second {
				continue
			}
			m.saveWorkspaceData(workspaceID)
			saved[workspaceID] = time.Now()

		case <-ticker.C:
			// Periodic save of all dirty workspaces.
			m.mu.RLock()
			workspaces := make([]string, 0, len(m.data))
			for id := range m.data {
				workspaces = append(workspaces, id)
			}
			m.mu.RUnlock()

			for _, id := range workspaces {
				if lastSave, exists := saved[id]; !exists || time.Since(lastSave) > 5*time.Minute {
					m.saveWorkspaceData(id)
					saved[id] = time.Now()
				}
			}
		}
	}
}

// saveWorkspaceData saves workspace data to disk.
func (m *Manager) saveWorkspaceData(workspaceID string) {
	m.mu.RLock()
	data, exists := m.data[workspaceID]
	m.mu.RUnlock()

	if !exists {
		return
	}

	// Sanitize workspaceID to prevent path traversal.
	safeID := filepath.Base(workspaceID)
	if safeID != workspaceID || safeID == "." || safeID == ".." {
		slog.Error("invalid workspace ID", "workspace_id", workspaceID)
		return
	}

	filename := filepath.Join(m.dataDir, fmt.Sprintf("%s.json.gz", safeID))
	tempFile := filename + ".tmp"

	file, err := os.OpenFile(tempFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		slog.Error("failed to create temp file", "error", err)
		return
	}
	// Remove defer - we'll close explicitly to handle errors properly

	gz := gzip.NewWriter(file)
	// Remove defer - we'll close explicitly to handle errors properly

	if err := json.NewEncoder(gz).Encode(data); err != nil {
		slog.Error("failed to encode state data", "error", err)
		gz.Close()   // Best effort cleanup
		file.Close() // Best effort cleanup
		if err := os.Remove(tempFile); err != nil {
			slog.Error("failed to remove temp file", "error", err)
		}
		return
	}

	if err := gz.Close(); err != nil {
		slog.Error("failed to close gzip writer", "error", err)
		file.Close() // Best effort cleanup
		if err := os.Remove(tempFile); err != nil {
			slog.Error("failed to remove temp file", "error", err)
		}
		return
	}

	if err := file.Close(); err != nil {
		slog.Error("failed to close file", "error", err)
		if err := os.Remove(tempFile); err != nil {
			slog.Error("failed to remove temp file", "error", err)
		}
		return
	}

	// Atomic rename.
	if err := os.Rename(tempFile, filename); err != nil {
		slog.Error("failed to rename temp file", "error", err)
		if err := os.Remove(tempFile); err != nil {
			slog.Error("failed to remove temp file", "error", err)
		}
		return
	}

	slog.Info("saved state", "workspace", workspaceID)
}
