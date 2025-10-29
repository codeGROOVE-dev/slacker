package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-github/v50/github"
)

// Test pure functions that don't require external dependencies.

func TestMatchesRepo(t *testing.T) {
	m := &Manager{}

	tests := []struct {
		name     string
		pattern  string
		repo     string
		expected bool
	}{
		{
			name:     "wildcard matches everything",
			pattern:  "*",
			repo:     "any-repo",
			expected: true,
		},
		{
			name:     "exact match",
			pattern:  "goose",
			repo:     "goose",
			expected: true,
		},
		{
			name:     "no match",
			pattern:  "goose",
			repo:     "slacker",
			expected: false,
		},
		{
			name:     "case sensitive - no match",
			pattern:  "Goose",
			repo:     "goose",
			expected: false,
		},
		{
			name:     "empty pattern does not match",
			pattern:  "",
			repo:     "repo",
			expected: false,
		},
		{
			name:     "empty repo does not match non-empty pattern",
			pattern:  "repo",
			repo:     "",
			expected: false,
		},
		{
			name:     "both empty is exact match",
			pattern:  "",
			repo:     "",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.matchesRepo(tt.pattern, tt.repo)
			if result != tt.expected {
				t.Errorf("matchesRepo(%q, %q) = %v, want %v",
					tt.pattern, tt.repo, result, tt.expected)
			}
		})
	}
}

func TestAutoDiscoverChannels(t *testing.T) {
	m := &Manager{}

	tests := []struct {
		name     string
		org      string
		repo     string
		expected []string
	}{
		{
			name:     "simple repo name",
			org:      "codeGROOVE-dev",
			repo:     "goose",
			expected: []string{"goose"},
		},
		{
			name:     "repo with dashes",
			org:      "codeGROOVE-dev",
			repo:     "my-service",
			expected: []string{"my-service"},
		},
		{
			name:     "uppercase repo becomes lowercase",
			org:      "codeGROOVE-dev",
			repo:     "MyRepo",
			expected: []string{"myrepo"},
		},
		{
			name:     "mixed case repo",
			org:      "codeGROOVE-dev",
			repo:     "CodeGROOVE",
			expected: []string{"codegroove"},
		},
		{
			name:     "repo with underscores",
			org:      "codeGROOVE-dev",
			repo:     "my_repo",
			expected: []string{"my_repo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.autoDiscoverChannels(tt.org, tt.repo)
			if len(result) != len(tt.expected) {
				t.Fatalf("autoDiscoverChannels() returned %d channels, want %d",
					len(result), len(tt.expected))
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("autoDiscoverChannels()[%d] = %q, want %q",
						i, result[i], tt.expected[i])
				}
			}
		})
	}
}

func TestCreateDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig()

	// Verify default values
	if cfg.Global.ReminderDMDelay != defaultReminderDMDelayMinutes {
		t.Errorf("expected default ReminderDMDelay %d, got %d",
			defaultReminderDMDelayMinutes, cfg.Global.ReminderDMDelay)
	}

	if !cfg.Global.DailyReminders {
		t.Error("expected DailyReminders to be enabled by default")
	}

	if cfg.Channels == nil {
		t.Error("expected Channels map to be initialized")
	}

	if cfg.Global.TeamID != "" {
		t.Errorf("expected empty TeamID, got %q", cfg.Global.TeamID)
	}

	if cfg.Global.EmailDomain != "" {
		t.Errorf("expected empty EmailDomain, got %q", cfg.Global.EmailDomain)
	}
}

func TestConfigCache_GetSet(t *testing.T) {
	cache := &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     5 * time.Minute,
	}

	// Test cache miss
	cfg, found := cache.get("test-org")
	if found {
		t.Error("expected cache miss for unknown org")
	}
	if cfg != nil {
		t.Error("expected nil config on cache miss")
	}

	// Set config
	testConfig := &RepoConfig{
		Global: struct {
			TeamID          string `yaml:"team_id"`
			EmailDomain     string `yaml:"email_domain"`
			ReminderDMDelay int    `yaml:"reminder_dm_delay"`
			DailyReminders  bool   `yaml:"daily_reminders"`
		}{
			TeamID:      "T123",
			EmailDomain: "example.com",
		},
	}

	cache.set("test-org", testConfig)

	// Test cache hit
	cfg, found = cache.get("test-org")
	if !found {
		t.Error("expected cache hit after setting config")
	}
	if cfg == nil {
		t.Fatal("expected non-nil config on cache hit")
	}
	if cfg.Global.TeamID != "T123" {
		t.Errorf("expected TeamID T123, got %q", cfg.Global.TeamID)
	}
}

func TestConfigCache_Expiration(t *testing.T) {
	cache := &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     50 * time.Millisecond, // Very short TTL for testing
	}

	testConfig := createDefaultConfig()
	cache.set("test-org", testConfig)

	// Immediate read should hit cache
	_, found := cache.get("test-org")
	if !found {
		t.Error("expected cache hit immediately after set")
	}

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	// Should now be expired
	_, found = cache.get("test-org")
	if found {
		t.Error("expected cache miss after TTL expiration")
	}
}

func TestConfigCache_Invalidate(t *testing.T) {
	cache := &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     5 * time.Minute,
	}

	// Set multiple configs
	cache.set("org1", createDefaultConfig())
	cache.set("org2", createDefaultConfig())

	// Verify both are cached
	_, found1 := cache.get("org1")
	_, found2 := cache.get("org2")
	if !found1 || !found2 {
		t.Fatal("expected both configs to be cached")
	}

	// Invalidate org1
	cache.invalidate("org1")

	// org1 should be gone, org2 should remain
	_, found1 = cache.get("org1")
	_, found2 = cache.get("org2")

	if found1 {
		t.Error("expected org1 to be invalidated")
	}
	if !found2 {
		t.Error("expected org2 to remain in cache")
	}
}

func TestConfigCache_InvalidateAll(t *testing.T) {
	cache := &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     5 * time.Minute,
	}

	// Set multiple configs
	cache.set("org1", createDefaultConfig())
	cache.set("org2", createDefaultConfig())
	cache.set("org3", createDefaultConfig())

	// Verify all are cached
	_, found1 := cache.get("org1")
	_, found2 := cache.get("org2")
	_, found3 := cache.get("org3")
	if !found1 || !found2 || !found3 {
		t.Fatal("expected all configs to be cached")
	}

	// Invalidate all
	cache.invalidateAll()

	// All should be gone
	_, found1 = cache.get("org1")
	_, found2 = cache.get("org2")
	_, found3 = cache.get("org3")

	if found1 || found2 || found3 {
		t.Error("expected all configs to be invalidated")
	}

	// Cache should still be functional
	cache.set("org4", createDefaultConfig())
	_, found4 := cache.get("org4")
	if !found4 {
		t.Error("expected cache to work after invalidateAll")
	}
}

func TestConfigCache_Stats(t *testing.T) {
	cache := &configCache{
		entries: make(map[string]configCacheEntry),
		ttl:     5 * time.Minute,
	}

	// Initial stats should be zero
	hits, misses := cache.stats()
	if hits != 0 || misses != 0 {
		t.Errorf("expected zero stats initially, got hits=%d misses=%d", hits, misses)
	}

	// Cache miss
	_, _ = cache.get("org1")
	hits, misses = cache.stats()
	if hits != 0 || misses != 1 {
		t.Errorf("expected 1 miss, got hits=%d misses=%d", hits, misses)
	}

	// Set and cache hit
	cache.set("org1", createDefaultConfig())
	_, _ = cache.get("org1")
	hits, misses = cache.stats()
	if hits != 1 || misses != 1 {
		t.Errorf("expected 1 hit and 1 miss, got hits=%d misses=%d", hits, misses)
	}

	// Multiple hits
	_, _ = cache.get("org1")
	_, _ = cache.get("org1")
	hits, misses = cache.stats()
	if hits != 3 || misses != 1 {
		t.Errorf("expected 3 hits and 1 miss, got hits=%d misses=%d", hits, misses)
	}

	// Multiple misses
	_, _ = cache.get("org2")
	_, _ = cache.get("org3")
	hits, misses = cache.stats()
	if hits != 3 || misses != 3 {
		t.Errorf("expected 3 hits and 3 misses, got hits=%d misses=%d", hits, misses)
	}
}

func TestManager_SettersAndGetters(t *testing.T) {
	m := New()

	// Test SetWorkspaceName
	m.SetWorkspaceName("test-workspace")
	if m.workspaceName != "test-workspace" {
		t.Errorf("expected workspace name 'test-workspace', got %q", m.workspaceName)
	}

	// Test Domain with no config
	domain := m.Domain("unknown-org")
	if domain != "" {
		t.Errorf("expected empty domain for unknown org, got %q", domain)
	}

	// Test DailyRemindersEnabled with no config (should default to true)
	enabled := m.DailyRemindersEnabled("unknown-org")
	if !enabled {
		t.Error("expected daily reminders enabled by default")
	}

	// Test ReminderDMDelay with no config (should return default)
	delay := m.ReminderDMDelay("unknown-org", "general")
	if delay != defaultReminderDMDelayMinutes {
		t.Errorf("expected default delay %d, got %d", defaultReminderDMDelayMinutes, delay)
	}

	// Test IsChannelMuted with no config
	muted := m.IsChannelMuted("unknown-org", "general")
	if muted {
		t.Error("expected channel not muted when no config exists")
	}
}

func TestManager_ConfigWithManualSetup(t *testing.T) {
	m := New()

	// Manually set a config without loading from GitHub
	testConfig := &RepoConfig{
		Channels: map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}{
			"dev": {
				Repos: []string{"goose", "slacker"},
				Mute:  false,
			},
			"muted-channel": {
				Repos: []string{"test"},
				Mute:  true,
			},
		},
		Global: struct {
			TeamID          string `yaml:"team_id"`
			EmailDomain     string `yaml:"email_domain"`
			ReminderDMDelay int    `yaml:"reminder_dm_delay"`
			DailyReminders  bool   `yaml:"daily_reminders"`
		}{
			TeamID:          "T123456",
			EmailDomain:     "example.com",
			ReminderDMDelay: 30,
			DailyReminders:  false,
		},
	}

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	// Test Domain
	domain := m.Domain("test-org")
	if domain != "example.com" {
		t.Errorf("expected domain 'example.com', got %q", domain)
	}

	// Test DailyRemindersEnabled
	enabled := m.DailyRemindersEnabled("test-org")
	if enabled {
		t.Error("expected daily reminders disabled")
	}

	// Test ReminderDMDelay with global setting
	delay := m.ReminderDMDelay("test-org", "unknown-channel")
	if delay != 30 {
		t.Errorf("expected delay 30, got %d", delay)
	}

	// Test IsChannelMuted
	muted := m.IsChannelMuted("test-org", "muted-channel")
	if !muted {
		t.Error("expected muted-channel to be muted")
	}

	notMuted := m.IsChannelMuted("test-org", "dev")
	if notMuted {
		t.Error("expected dev channel not to be muted")
	}

	// Test WorkspaceName
	workspaceName := m.WorkspaceName("test-org")
	if workspaceName != "T123456" {
		t.Errorf("expected workspace name 'T123456', got %q", workspaceName)
	}
}

func TestManager_ReminderDMDelayWithChannelOverride(t *testing.T) {
	m := New()

	// Create config with channel-specific override
	channelOverride := 15
	testConfig := &RepoConfig{
		Channels: map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}{
			"urgent": {
				ReminderDMDelay: &channelOverride,
				Repos:           []string{"critical-service"},
			},
		},
		Global: struct {
			TeamID          string `yaml:"team_id"`
			EmailDomain     string `yaml:"email_domain"`
			ReminderDMDelay int    `yaml:"reminder_dm_delay"`
			DailyReminders  bool   `yaml:"daily_reminders"`
		}{
			ReminderDMDelay: 60, // Global default
		},
	}

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	// Test channel-specific override
	delay := m.ReminderDMDelay("test-org", "urgent")
	if delay != 15 {
		t.Errorf("expected channel override delay 15, got %d", delay)
	}

	// Test fallback to global setting
	delay = m.ReminderDMDelay("test-org", "other-channel")
	if delay != 60 {
		t.Errorf("expected global delay 60, got %d", delay)
	}
}

func TestManager_ChannelsForRepoWithWildcard(t *testing.T) {
	m := New()

	testConfig := &RepoConfig{
		Channels: map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}{
			"all-repos": {
				Repos: []string{"*"}, // Wildcard matches everything
			},
			"specific": {
				Repos: []string{"goose"},
			},
		},
	}

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	// Test wildcard match
	channels := m.ChannelsForRepo("test-org", "any-repo")
	if len(channels) < 1 {
		t.Fatal("expected at least 1 channel for wildcard match")
	}

	// Should include the wildcard channel
	foundWildcard := false
	for _, ch := range channels {
		if ch == "all-repos" {
			foundWildcard = true
			break
		}
	}
	if !foundWildcard {
		t.Error("expected wildcard channel 'all-repos' to match")
	}

	// Test specific repo with multiple matches
	channels = m.ChannelsForRepo("test-org", "goose")
	if len(channels) < 2 {
		t.Fatalf("expected at least 2 channels (wildcard + specific), got %d", len(channels))
	}

	foundSpecific := false
	foundWildcard = false
	for _, ch := range channels {
		if ch == "specific" {
			foundSpecific = true
		}
		if ch == "all-repos" {
			foundWildcard = true
		}
	}
	if !foundSpecific || !foundWildcard {
		t.Errorf("expected both 'specific' and 'all-repos' channels, got %v", channels)
	}
}

func TestManager_ChannelsForRepoWithMuting(t *testing.T) {
	m := New()

	testConfig := &RepoConfig{
		Channels: map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}{
			"active": {
				Repos: []string{"goose"},
				Mute:  false,
			},
			"muted": {
				Repos: []string{"goose"},
				Mute:  true,
			},
			"goose": { // Auto-discovered channel can be muted
				Mute: true,
			},
		},
	}

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	channels := m.ChannelsForRepo("test-org", "goose")

	// Should only include active channel, not muted ones
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel (muted channels should be excluded), got %d: %v", len(channels), channels)
	}

	if channels[0] != "active" {
		t.Errorf("expected 'active' channel, got %q", channels[0])
	}
}

func TestManager_CacheStats(t *testing.T) {
	m := New()

	// Initial stats should be zero
	hits, misses := m.CacheStats()
	if hits != 0 || misses != 0 {
		t.Errorf("expected zero cache stats initially, got hits=%d misses=%d", hits, misses)
	}

	// Trigger cache operations by setting and getting
	m.cache.set("test-org", createDefaultConfig())
	_, _ = m.cache.get("test-org")
	_, _ = m.cache.get("unknown-org")

	hits, misses = m.CacheStats()
	if hits != 1 {
		t.Errorf("expected 1 cache hit, got %d", hits)
	}
	if misses != 1 {
		t.Errorf("expected 1 cache miss, got %d", misses)
	}
}

func TestManager_InvalidateConfig(t *testing.T) {
	m := New()

	// Set a config in cache
	m.cache.set("test-org", createDefaultConfig())

	// Verify it's cached
	_, found := m.cache.get("test-org")
	if !found {
		t.Fatal("expected config to be cached")
	}

	// Invalidate
	m.InvalidateConfig("test-org")

	// Should be removed from cache
	_, found = m.cache.get("test-org")
	if found {
		t.Error("expected config to be invalidated")
	}
}

func TestManager_InvalidateAllConfigs(t *testing.T) {
	m := New()

	// Set multiple configs in cache
	m.cache.set("org1", createDefaultConfig())
	m.cache.set("org2", createDefaultConfig())

	// Invalidate all
	m.InvalidateAllConfigs()

	// All should be removed
	_, found1 := m.cache.get("org1")
	_, found2 := m.cache.get("org2")

	if found1 || found2 {
		t.Error("expected all configs to be invalidated")
	}
}

func TestManager_SetGitHubClientAndConfig(t *testing.T) {
	m := New()

	// Test Config with no config loaded
	cfg, exists := m.Config("test-org")
	if exists {
		t.Error("expected config not to exist for unknown org")
	}
	if cfg != nil {
		t.Error("expected nil config for unknown org")
	}

	// Manually set a config
	testConfig := createDefaultConfig()
	testConfig.Global.TeamID = "T123"

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	// Test Config returns the config
	cfg, exists = m.Config("test-org")
	if !exists {
		t.Fatal("expected config to exist after setting")
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Global.TeamID != "T123" {
		t.Errorf("expected TeamID T123, got %q", cfg.Global.TeamID)
	}

	// Test SetGitHubClient (coverage only - behavior tested in LoadConfig tests)
	mockClient := &github.Client{}
	m.SetGitHubClient("test-org", mockClient)

	// Verify client was set
	m.mu.RLock()
	client := m.clients["test-org"]
	m.mu.RUnlock()

	if client != mockClient {
		t.Error("expected SetGitHubClient to store the client")
	}
}

func TestManager_WorkspaceNameEdgeCases(t *testing.T) {
	m := New()

	// Test WorkspaceName with config that has empty TeamID
	emptyConfig := createDefaultConfig()
	emptyConfig.Global.TeamID = ""

	m.mu.Lock()
	m.configs["test-org"] = emptyConfig
	m.mu.Unlock()

	workspaceName := m.WorkspaceName("test-org")
	if workspaceName != "" {
		t.Errorf("expected empty workspace name, got %q", workspaceName)
	}
}

func TestManager_IsChannelMutedCaseInsensitive(t *testing.T) {
	m := New()

	testConfig := &RepoConfig{
		Channels: map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}{
			"TestChannel": { // Mixed case in config
				Mute: true,
			},
		},
	}

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	// Exact match (case-sensitive lookup)
	muted := m.IsChannelMuted("test-org", "TestChannel")
	if !muted {
		t.Error("expected TestChannel to be muted")
	}

	// Different case - won't match (IsChannelMuted is case-sensitive)
	notMuted := m.IsChannelMuted("test-org", "testchannel")
	if notMuted {
		t.Error("expected case-sensitive lookup to not match")
	}
}

func TestManager_ReminderDMDelayZeroGlobal(t *testing.T) {
	m := New()

	testConfig := &RepoConfig{
		Channels: map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}{},
		Global: struct {
			TeamID          string `yaml:"team_id"`
			EmailDomain     string `yaml:"email_domain"`
			ReminderDMDelay int    `yaml:"reminder_dm_delay"`
			DailyReminders  bool   `yaml:"daily_reminders"`
		}{
			ReminderDMDelay: 0, // Explicitly disabled
		},
	}

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	// Should fall back to default when global is 0
	delay := m.ReminderDMDelay("test-org", "any-channel")
	if delay != defaultReminderDMDelayMinutes {
		t.Errorf("expected default delay %d when global is 0, got %d", defaultReminderDMDelayMinutes, delay)
	}
}

func TestManager_ReminderDMDelayChannelZero(t *testing.T) {
	m := New()

	zeroDelay := 0
	testConfig := &RepoConfig{
		Channels: map[string]struct {
			ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
			Repos           []string `yaml:"repos"`
			Mute            bool     `yaml:"mute"`
		}{
			"urgent": {
				ReminderDMDelay: &zeroDelay, // Explicitly disabled for this channel
			},
		},
		Global: struct {
			TeamID          string `yaml:"team_id"`
			EmailDomain     string `yaml:"email_domain"`
			ReminderDMDelay int    `yaml:"reminder_dm_delay"`
			DailyReminders  bool   `yaml:"daily_reminders"`
		}{
			ReminderDMDelay: 60,
		},
	}

	m.mu.Lock()
	m.configs["test-org"] = testConfig
	m.mu.Unlock()

	// Channel-specific 0 should be returned (not default)
	delay := m.ReminderDMDelay("test-org", "urgent")
	if delay != 0 {
		t.Errorf("expected channel delay 0 (disabled), got %d", delay)
	}
}

func TestManager_ChannelsForRepoNoConfig(t *testing.T) {
	m := New()

	// No config loaded - should auto-discover
	channels := m.ChannelsForRepo("test-org", "goose")
	if len(channels) != 1 {
		t.Fatalf("expected 1 auto-discovered channel, got %d", len(channels))
	}
	if channels[0] != "goose" {
		t.Errorf("expected auto-discovered channel 'goose', got %q", channels[0])
	}
}

func TestManager_LoadConfigNoClient(t *testing.T) {
	m := New()
	ctx := context.Background()

	// LoadConfig should fail if no GitHub client is set
	err := m.LoadConfig(ctx, "test-org")
	if err == nil {
		t.Error("expected error when GitHub client not set")
	}
	if err != nil && !contains(err.Error(), "github client not initialized") {
		t.Errorf("expected 'github client not initialized' error, got %v", err)
	}
}

func TestManager_LoadConfigFromCache(t *testing.T) {
	m := New()
	ctx := context.Background()

	// Pre-populate cache
	cachedConfig := createDefaultConfig()
	cachedConfig.Global.TeamID = "T999"
	m.cache.set("test-org", cachedConfig)

	// LoadConfig should use cached config (no GitHub client needed)
	err := m.LoadConfig(ctx, "test-org")
	if err != nil {
		t.Fatalf("unexpected error loading from cache: %v", err)
	}

	// Verify config was loaded from cache
	cfg, exists := m.Config("test-org")
	if !exists {
		t.Fatal("expected config to exist")
	}
	if cfg.Global.TeamID != "T999" {
		t.Errorf("expected cached TeamID T999, got %q", cfg.Global.TeamID)
	}

	// Verify cache stats show a hit
	hits, _ := m.CacheStats()
	if hits < 1 {
		t.Error("expected at least 1 cache hit")
	}
}

func TestManager_ReloadConfig(t *testing.T) {
	m := New()
	ctx := context.Background()

	// Pre-populate cache
	oldConfig := createDefaultConfig()
	oldConfig.Global.TeamID = "T111"
	m.cache.set("test-org", oldConfig)

	// Verify config is in cache
	_, found := m.cache.get("test-org")
	if !found {
		t.Fatal("expected config to be in cache")
	}

	// ReloadConfig should invalidate cache and call LoadConfig
	// Since we don't have a GitHub client, this will fail
	err := m.ReloadConfig(ctx, "test-org")
	if err == nil {
		t.Error("expected error when reloading without GitHub client")
	}

	// Verify cache was invalidated (will be a cache miss now)
	cfg, found := m.cache.get("test-org")
	if found && cfg != nil && cfg.Global.TeamID == "T111" {
		t.Error("expected cache to be invalidated by ReloadConfig")
	}
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// createTestGitHubClient creates a GitHub client with a mock HTTP server.
func createTestGitHubClient(handler http.HandlerFunc) (*github.Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	client := github.NewClient(nil)
	client.BaseURL = must(client.BaseURL.Parse(server.URL + "/"))
	return client, server
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestManager_LoadConfigValidYAML(t *testing.T) {
	validYAML := `
global:
  team_id: T123456
  email_domain: example.com
  reminder_dm_delay: 30
  daily_reminders: true
channels:
  dev:
    repos:
      - goose
      - slacker
  all:
    repos:
      - "*"
`

	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test-org/.codeGROOVE/contents/slack.yaml" {
			content := base64.StdEncoding.EncodeToString([]byte(validYAML))
			encoding := "base64"
			response := github.RepositoryContent{
				Type:     github.String("file"),
				Content:  &content,
				Encoding: &encoding,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}

	client, server := createTestGitHubClient(handler)
	defer server.Close()

	m := New()
	m.SetGitHubClient("test-org", client)

	ctx := context.Background()
	err := m.LoadConfig(ctx, "test-org")
	if err != nil {
		t.Fatalf("unexpected error loading valid config: %v", err)
	}

	// Verify config was loaded
	cfg, exists := m.Config("test-org")
	if !exists {
		t.Fatal("expected config to exist after loading")
	}
	if cfg.Global.TeamID != "T123456" {
		t.Errorf("expected TeamID T123456, got %q", cfg.Global.TeamID)
	}
	if cfg.Global.EmailDomain != "example.com" {
		t.Errorf("expected email domain example.com, got %q", cfg.Global.EmailDomain)
	}
	if cfg.Global.ReminderDMDelay != 30 {
		t.Errorf("expected reminder delay 30, got %d", cfg.Global.ReminderDMDelay)
	}
	if len(cfg.Channels) != 2 {
		t.Errorf("expected 2 channels, got %d", len(cfg.Channels))
	}
}

func TestManager_LoadConfig404NotFound(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}

	client, server := createTestGitHubClient(handler)
	defer server.Close()

	m := New()
	m.SetGitHubClient("test-org", client)

	ctx := context.Background()
	err := m.LoadConfig(ctx, "test-org")
	if err != nil {
		t.Fatalf("expected graceful degradation on 404, got error: %v", err)
	}

	// Should have default config
	cfg, exists := m.Config("test-org")
	if !exists {
		t.Fatal("expected default config to exist")
	}
	if cfg.Global.ReminderDMDelay != defaultReminderDMDelayMinutes {
		t.Errorf("expected default delay, got %d", cfg.Global.ReminderDMDelay)
	}
	if !cfg.Global.DailyReminders {
		t.Error("expected daily reminders enabled by default")
	}
}

func TestManager_LoadConfigInvalidYAML(t *testing.T) {
	invalidYAML := `
global:
  - this is not valid yaml
  - it should be a map
channels: [1, 2, 3]
`

	handler := func(w http.ResponseWriter, r *http.Request) {
		content := base64.StdEncoding.EncodeToString([]byte(invalidYAML))
		encoding := "base64"
		response := github.RepositoryContent{
			Type:     github.String("file"),
			Content:  &content,
			Encoding: &encoding,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}

	client, server := createTestGitHubClient(handler)
	defer server.Close()

	m := New()
	m.SetGitHubClient("test-org", client)

	ctx := context.Background()
	err := m.LoadConfig(ctx, "test-org")
	if err != nil {
		t.Fatalf("expected graceful degradation on invalid YAML, got error: %v", err)
	}

	// Should fall back to default config
	cfg, exists := m.Config("test-org")
	if !exists {
		t.Fatal("expected default config to exist")
	}
	if cfg.Global.ReminderDMDelay != defaultReminderDMDelayMinutes {
		t.Errorf("expected default delay, got %d", cfg.Global.ReminderDMDelay)
	}
}

func TestManager_LoadConfigCodeGROOVEProdConfig(t *testing.T) {
	// Test with actual production config from codeGROOVE-dev/.codeGROOVE/slack.yaml
	prodYAML := `global:
    team_id: T09CJ7X7T7Y
    email_domain: codegroove.dev
    reminder_dm_delay: 1

channels:
    goose:
      mute: true

    all-codegroove:
      repos:
        - "*"

    social:
      repos:
        - goose
        - sprinkler
        - slacker
`

	handler := func(w http.ResponseWriter, r *http.Request) {
		content := base64.StdEncoding.EncodeToString([]byte(prodYAML))
		encoding := "base64"
		response := github.RepositoryContent{
			Type:     github.String("file"),
			Content:  &content,
			Encoding: &encoding,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("failed to encode response: %v", err)
		}
	}

	client, server := createTestGitHubClient(handler)
	defer server.Close()

	m := New()
	m.SetGitHubClient("codeGROOVE-dev", client)

	ctx := context.Background()
	err := m.LoadConfig(ctx, "codeGROOVE-dev")
	if err != nil {
		t.Fatalf("unexpected error loading production config: %v", err)
	}

	// Verify config was loaded correctly
	cfg, exists := m.Config("codeGROOVE-dev")
	if !exists {
		t.Fatal("expected config to exist after loading")
	}
	if cfg.Global.TeamID != "T09CJ7X7T7Y" {
		t.Errorf("expected TeamID T09CJ7X7T7Y, got %q", cfg.Global.TeamID)
	}
	if cfg.Global.EmailDomain != "codegroove.dev" {
		t.Errorf("expected email domain codegroove.dev, got %q", cfg.Global.EmailDomain)
	}
	if cfg.Global.ReminderDMDelay != 1 {
		t.Errorf("expected reminder delay 1 minute, got %d", cfg.Global.ReminderDMDelay)
	}
	if len(cfg.Channels) != 3 {
		t.Errorf("expected 3 channels, got %d", len(cfg.Channels))
	}

	// Verify goose channel is muted
	gooseChannel, exists := cfg.Channels["goose"]
	if !exists {
		t.Error("expected goose channel to exist")
	}
	if !gooseChannel.Mute {
		t.Error("expected goose channel to be muted")
	}

	// Verify all-codegroove has wildcard
	allChannel, exists := cfg.Channels["all-codegroove"]
	if !exists {
		t.Error("expected all-codegroove channel to exist")
	}
	if len(allChannel.Repos) != 1 || allChannel.Repos[0] != "*" {
		t.Errorf("expected all-codegroove to have wildcard repo, got %v", allChannel.Repos)
	}

	// Verify social channel repos
	socialChannel, exists := cfg.Channels["social"]
	if !exists {
		t.Error("expected social channel to exist")
	}
	expectedRepos := []string{"goose", "sprinkler", "slacker"}
	if len(socialChannel.Repos) != len(expectedRepos) {
		t.Errorf("expected %d repos in social channel, got %d", len(expectedRepos), len(socialChannel.Repos))
	}
	for i, repo := range expectedRepos {
		if i >= len(socialChannel.Repos) || socialChannel.Repos[i] != repo {
			t.Errorf("expected repo %q at index %d in social channel, got %v", repo, i, socialChannel.Repos)
		}
	}

	// Verify ReminderDMDelay returns correct value
	delay := m.ReminderDMDelay("codeGROOVE-dev", "social")
	if delay != 1 {
		t.Errorf("expected ReminderDMDelay to return 1 minute, got %d", delay)
	}
}

func TestManager_LoadConfigEmptyContent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Return a response with nil Content
		response := github.RepositoryContent{
			Type: github.String("file"),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}

	client, server := createTestGitHubClient(handler)
	defer server.Close()

	m := New()
	m.SetGitHubClient("test-org", client)

	ctx := context.Background()
	err := m.LoadConfig(ctx, "test-org")
	if err != nil {
		t.Fatalf("expected graceful degradation on empty content, got error: %v", err)
	}

	// Should use default config
	_, exists := m.Config("test-org")
	if !exists {
		t.Fatal("expected default config to exist")
	}
}
