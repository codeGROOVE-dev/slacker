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

// UserPreferences holds user notification preferences.
type UserPreferences struct {
	RealTimeNotifications bool          `json:"real_time_notifications"`
	ChannelNotifyDelay    time.Duration `json:"channel_notify_delay"` // 15min, 30min, 60min, 2hr
	DailyReminders        bool          `json:"daily_reminders"`
	Timezone              string        `json:"timezone"`
	LastNotified          time.Time     `json:"last_notified"`
}

// PRState represents the current state of a PR.
type PRState struct {
	Owner        string    `json:"owner"`
	Repo         string    `json:"repo"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	Author       string    `json:"author"`
	State        string    `json:"state"` // test_tube, broken_heart, hourglass, etc.
	BlockedOn    []string  `json:"blocked_on"`
	Reviewers    []string  `json:"reviewers"`
	ThreadTS     string    `json:"thread_ts"`  // Slack thread timestamp
	ChannelID    string    `json:"channel_id"` // Slack channel ID
	LastUpdated  time.Time `json:"last_updated"`
	LastNotified time.Time `json:"last_notified"`
}

// WorkspaceData holds data for a Slack workspace.
type WorkspaceData struct {
	WorkspaceID string                     `json:"workspace_id"`
	Users       map[string]UserPreferences `json:"users"`    // userID -> preferences
	PRs         map[string]*PRState        `json:"prs"`      // "owner/repo#number" -> state
	UserPRs     map[string][]string        `json:"user_prs"` // userID -> list of PR keys
	LastUpdated time.Time                  `json:"last_updated"`
}

// Manager manages application state with file persistence.
type Manager struct {
	mu       sync.RWMutex
	dataDir  string
	data     map[string]*WorkspaceData // workspaceID -> data
	saveChan chan string               // channel for async saves
}

// New creates a new state manager.
func New(dataDir string) *Manager {
	m := &Manager{
		dataDir:  dataDir,
		data:     make(map[string]*WorkspaceData),
		saveChan: make(chan string, 100),
	}

	// Create data directory if it doesn't exist.
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Error("failed to create data directory", "error", err)
	}

	// Start background save worker.
	go m.saveWorker()

	return m
}

// GetUserPreferences returns user preferences.
func (m *Manager) GetUserPreferences(workspaceID, userID string) UserPreferences {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Load workspace data if not in memory.
	if _, exists := m.data[workspaceID]; !exists {
		m.mu.RUnlock()
		m.loadWorkspaceData(workspaceID)
		m.mu.RLock()
	}

	workspace, exists := m.data[workspaceID]
	if !exists || workspace.Users == nil {
		// Return defaults.
		return UserPreferences{
			RealTimeNotifications: true,
			ChannelNotifyDelay:    30 * time.Minute,
			DailyReminders:        true,
		}
	}

	prefs, exists := workspace.Users[userID]
	if !exists {
		// Return defaults.
		return UserPreferences{
			RealTimeNotifications: true,
			ChannelNotifyDelay:    30 * time.Minute,
			DailyReminders:        true,
		}
	}

	return prefs
}

// SetUserPreferences updates user preferences.
func (m *Manager) SetUserPreferences(workspaceID, userID string, prefs UserPreferences) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workspace := m.ensureWorkspace(workspaceID)
	if workspace.Users == nil {
		workspace.Users = make(map[string]UserPreferences)
	}
	workspace.Users[userID] = prefs
	workspace.LastUpdated = time.Now()

	// Queue save.
	select {
	case m.saveChan <- workspaceID:
	default:
		// Channel full, save will happen soon anyway.
	}
}

// GetPRState returns the state of a PR.
func (m *Manager) GetPRState(workspaceID, owner, repo string, number int) (*PRState, bool) {
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
		if !contains(workspace.UserPRs[userID], key) {
			workspace.UserPRs[userID] = append(workspace.UserPRs[userID], key)
		}
	}

	// Queue save.
	select {
	case m.saveChan <- workspaceID:
	default:
	}
}

// GetUserPRs returns PRs associated with a user.
func (m *Manager) GetUserPRs(workspaceID, userID string) []*PRState {
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

// UpdateLastNotified updates the last notified time for a user.
func (m *Manager) UpdateLastNotified(workspaceID, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workspace := m.ensureWorkspace(workspaceID)
	if workspace.Users == nil {
		workspace.Users = make(map[string]UserPreferences)
	}

	prefs := workspace.Users[userID]
	prefs.LastNotified = time.Now()
	workspace.Users[userID] = prefs

	// Queue save.
	select {
	case m.saveChan <- workspaceID:
	default:
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
		WorkspaceID: workspaceID,
		Users:       make(map[string]UserPreferences),
		PRs:         make(map[string]*PRState),
		UserPRs:     make(map[string][]string),
		LastUpdated: time.Now(),
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
	filename := filepath.Join(m.dataDir, fmt.Sprintf("%s.json.gz", workspaceID))

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

	slog.Info("loaded state", "workspace", workspaceID, "users", len(data.Users), "prs", len(data.PRs))
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

	filename := filepath.Join(m.dataDir, fmt.Sprintf("%s.json.gz", workspaceID))
	tempFile := filename + ".tmp"

	file, err := os.Create(tempFile)
	if err != nil {
		slog.Error("failed to create temp file", "error", err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
		}
	}()

	gz := gzip.NewWriter(file)
	defer func() {
		if err := gz.Close(); err != nil {
			slog.Error("failed to close gzip reader", "error", err)
		}
	}()

	if err := json.NewEncoder(gz).Encode(data); err != nil {
		slog.Error("failed to encode state data", "error", err)
		if err := os.Remove(tempFile); err != nil {
			slog.Error("failed to remove temp file", "error", err)
		}
		return
	}

	if err := gz.Close(); err != nil {
		slog.Error("failed to close gzip writer", "error", err)
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

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
