package config

import (
	"testing"
	"time"
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
