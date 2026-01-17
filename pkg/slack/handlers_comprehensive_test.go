package slack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
	"github.com/slack-go/slack"
)

func TestHomeHandler_HandleAppHomeOpened_noWorkspaceOrgs(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{ID: userID, TZ: "UTC"}, nil
		},
		publishViewFunc: func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
			return &slack.ViewResponse{}, nil
		},
	}

	slackManager := NewManager("test-secret")
	slackManager.mu.Lock()
	slackManager.clients["T123"] = &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	slackManager.mu.Unlock()

	githubManager := newTestGitHubManager()
	configManager := newTestConfigManager()
	// No configs - no workspace orgs

	handler := &HomeHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		configManager:  configManager,
		reverseMapping: newTestUserMapper(),
	}

	err := handler.HandleAppHomeOpened(context.Background(), "T123", "U123")

	// Should succeed with placeholder view
	if err != nil {
		t.Errorf("HandleAppHomeOpened(T123, U123) with no workspace orgs = %v, want nil", err)
	}
}

func TestHomeHandler_HandleAppHomeOpened_invalidAuth_cacheInvalidation(t *testing.T) {
	t.Parallel()

	callCount := 0
	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{ID: userID, TZ: "UTC"}, nil
		},
		publishViewFunc: func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
			callCount++
			// Always return invalid_auth to test cache invalidation
			return nil, errors.New("invalid_auth")
		},
	}

	slackManager := NewManager("test-secret")
	slackManager.mu.Lock()
	slackManager.clients["T123"] = &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	slackManager.mu.Unlock()

	githubManager := newTestGitHubManager()
	githubManager.addOrg("org1", &github.Client{})

	configManager := newTestConfigManager()
	cfg := &config.RepoConfig{}
	cfg.Global.TeamID = "T123"
	cfg.Global.EmailDomain = "example.com"
	configManager.setConfig("org1", cfg)

	userMapper := newTestUserMapper()
	userMapper.setLookupFunc(func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error) {
		// Fail to map user so we hit publishPlaceholderHome which calls publishView
		return nil, errors.New("user not found")
	})

	handler := &HomeHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		configManager:  configManager,
		stateStore:     &state.MemoryStore{},
		reverseMapping: userMapper,
	}

	err := handler.HandleAppHomeOpened(context.Background(), "T123", "U123")

	// Should detect invalid_auth and attempt retry, but will fail to get new client from GSM in test
	if err == nil {
		t.Error("HandleAppHomeOpened(T123, U123) with persistent invalid_auth = nil, want error")
	}

	// Verify first attempt was made (callCount >= 1)
	if callCount < 1 {
		t.Errorf("HandleAppHomeOpened(T123, U123) made %d publishView calls, want at least 1", callCount)
	}

	// Verify cache was invalidated by checking error message includes retry-related text
	if !strings.Contains(err.Error(), "failed to get Slack client") && !strings.Contains(err.Error(), "failed to fetch token") {
		t.Errorf("HandleAppHomeOpened(T123, U123) error = %v, want error indicating retry attempt", err)
	}
}

func TestHomeHandler_tryHandleAppHomeOpened_userMappingFailure(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{ID: userID, TZ: "UTC"}, nil
		},
		publishViewFunc: func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
			return &slack.ViewResponse{}, nil
		},
	}

	slackManager := NewManager("test-secret")
	slackManager.mu.Lock()
	slackManager.clients["T123"] = &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	slackManager.mu.Unlock()

	githubManager := newTestGitHubManager()
	githubManager.addOrg("org1", nil)

	configManager := newTestConfigManager()
	cfg := &config.RepoConfig{}
	cfg.Global.TeamID = "T123"
	cfg.Global.EmailDomain = "example.com"
	configManager.setConfig("org1", cfg)

	userMapper := newTestUserMapper()
	userMapper.setLookupFunc(func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error) {
		// Fail to find user - will fall back to placeholder
		return nil, errors.New("user not found")
	})

	handler := &HomeHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		configManager:  configManager,
		stateStore:     &state.MemoryStore{},
		reverseMapping: userMapper,
	}

	err := handler.HandleAppHomeOpened(context.Background(), "T123", "U123")

	// Should succeed with placeholder
	if err != nil {
		t.Errorf("HandleAppHomeOpened(T123, U123) with failed user mapping = %v, want nil (placeholder)", err)
	}
}

func TestHomeHandler_workspaceOrgs_configWithOverrides(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{ID: userID, TZ: "UTC"}, nil
		},
		publishViewFunc: func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
			return &slack.ViewResponse{}, nil
		},
	}

	slackManager := NewManager("test-secret")
	slackManager.mu.Lock()
	slackManager.clients["T123"] = &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	slackManager.mu.Unlock()

	githubManager := newTestGitHubManager()
	githubManager.addOrg("org1", &github.Client{})

	configManager := newTestConfigManager()
	cfg := &config.RepoConfig{
		Users: map[string]string{
			"github-user-1": "user1@example.com",
			"github-user-2": "user2@example.com",
		},
	}
	cfg.Global.TeamID = "T123"
	cfg.Global.EmailDomain = "example.com"
	configManager.setConfig("org1", cfg)

	overridesSet := false
	userMapper := newTestUserMapper()
	userMapper.setOverridesFunc = func(overrides map[string]string) {
		overridesSet = true
		if len(overrides) != 2 {
			t.Errorf("SetOverrides called with %d overrides, want 2", len(overrides))
		}
	}
	userMapper.setLookupFunc(func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error) {
		return nil, errors.New("user not found")
	})

	handler := &HomeHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		configManager:  configManager,
		stateStore:     &state.MemoryStore{},
		reverseMapping: userMapper,
	}

	_ = handler.HandleAppHomeOpened(context.Background(), "T123", "U123")

	if !overridesSet {
		t.Error("HandleAppHomeOpened(T123, U123) did not call SetOverrides for config users")
	}
}

func TestReportHandler_HandleReportCommand_mockAPIClient(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
			return &slack.TeamInfo{
				ID:     "T123",
				Name:   "Test Workspace",
				Domain: "test-workspace",
			}, nil
		},
	}

	slackManager := NewManager("test-secret")
	slackManager.mu.Lock()
	slackManager.clients["T123"] = &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	slackManager.mu.Unlock()

	handler := &ReportHandler{
		slackManager:   slackManager,
		githubManager:  newTestGitHubManager(),
		stateStore:     &state.MemoryStore{},
		reverseMapping: newTestUserMapper(),
	}

	err := handler.HandleReportCommand(context.Background(), "T123", "U123")

	// Mock clients return nil from API(), which the handler should handle gracefully
	if err == nil {
		t.Error("HandleReportCommand(T123, U123) with mock client = nil, want error")
	}

	expectedErr := "failed to get Slack API client"
	if err != nil && !strings.Contains(err.Error(), expectedErr) {
		t.Errorf("HandleReportCommand(T123, U123) error = %v, want error containing %q", err, expectedErr)
	}
}

func TestReportHandler_HandleReportCommand_differentGitHubUsernames(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
			return &slack.TeamInfo{
				ID:     "T123",
				Name:   "Test Workspace",
				Domain: "test-workspace",
			}, nil
		},
	}

	slackManager := NewManager("test-secret")
	slackManager.mu.Lock()
	slackManager.clients["T123"] = &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	slackManager.mu.Unlock()

	githubManager := newTestGitHubManager()
	githubManager.addOrg("org1", nil)
	githubManager.addOrg("org2", nil)

	userMapper := newTestUserMapper()
	userMapper.setLookupFunc(func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error) {
		// Different GitHub username in each org (conflict scenario)
		if org == "org1" {
			return &usermapping.ReverseMapping{
				GitHubUsername: "user1",
				MatchMethod:    "email",
				Confidence:     90,
			}, nil
		}
		if org == "org2" {
			return &usermapping.ReverseMapping{
				GitHubUsername: "user2", // Different username - should skip this org
				MatchMethod:    "email",
				Confidence:     90,
			}, nil
		}
		return nil, errors.New("user not found")
	})

	handler := &ReportHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		stateStore:     &state.MemoryStore{},
		reverseMapping: userMapper,
	}

	err := handler.HandleReportCommand(context.Background(), "T123", "U123")

	// Will fail because API() returns nil for mocks
	if err == nil {
		t.Error("HandleReportCommand(T123, U123) = nil, want error (API returns nil for mocks)")
	}
}

func TestHomeHandler_tryHandleAppHomeOpened_noConfigForOrg(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{ID: userID, TZ: "UTC"}, nil
		},
		publishViewFunc: func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
			return &slack.ViewResponse{}, nil
		},
	}

	slackManager := NewManager("test-secret")
	slackManager.mu.Lock()
	slackManager.clients["T123"] = &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}
	slackManager.mu.Unlock()

	githubManager := newTestGitHubManager()
	githubManager.addOrg("org1", &github.Client{})

	configManager := newTestConfigManager()
	// Don't set config for org1 - will skip user mapping for this org

	userMapper := newTestUserMapper()
	userMapper.setLookupFunc(func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error) {
		t.Error("LookupGitHub should not be called when org has no config")
		return nil, errors.New("unexpected call")
	})

	handler := &HomeHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		configManager:  configManager,
		stateStore:     &state.MemoryStore{},
		reverseMapping: userMapper,
	}

	err := handler.HandleAppHomeOpened(context.Background(), "T123", "U123")

	// Should succeed with placeholder (no orgs have config matching this workspace)
	if err != nil {
		t.Errorf("HandleAppHomeOpened(T123, U123) with no org configs = %v, want nil (placeholder)", err)
	}
}
