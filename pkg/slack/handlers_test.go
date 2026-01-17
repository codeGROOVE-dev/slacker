package slack

import (
	"context"
	"errors"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
	"github.com/slack-go/slack"
)

func TestNewHomeHandler(t *testing.T) {
	t.Parallel()

	slackManager := NewManager("test-secret")
	githubManager := newTestGitHubManager()
	configManager := newTestConfigManager()
	stateStore := &state.MemoryStore{}
	userMapper := newTestUserMapper()

	got := NewHomeHandler(slackManager, githubManager, configManager, stateStore, userMapper)

	if got == nil {
		t.Fatal("NewHomeHandler() = nil, want non-nil")
	}

	if got.slackManager != slackManager {
		t.Error("NewHomeHandler().slackManager incorrectly set")
	}
	if got.githubManager == nil {
		t.Error("NewHomeHandler().githubManager = nil, want non-nil")
	}
	if got.configManager == nil {
		t.Error("NewHomeHandler().configManager = nil, want non-nil")
	}
	if got.stateStore != stateStore {
		t.Error("NewHomeHandler().stateStore incorrectly set")
	}
	if got.reverseMapping == nil {
		t.Error("NewHomeHandler().reverseMapping = nil, want non-nil")
	}
}

func TestNewReportHandler(t *testing.T) {
	t.Parallel()

	slackManager := NewManager("test-secret")
	githubManager := newTestGitHubManager()
	stateStore := &state.MemoryStore{}
	userMapper := newTestUserMapper()

	got := NewReportHandler(slackManager, githubManager, stateStore, userMapper)

	if got == nil {
		t.Fatal("NewReportHandler() = nil, want non-nil")
	}

	if got.slackManager != slackManager {
		t.Error("NewReportHandler().slackManager incorrectly set")
	}
	if got.githubManager == nil {
		t.Error("NewReportHandler().githubManager = nil, want non-nil")
	}
	if got.stateStore != stateStore {
		t.Error("NewReportHandler().stateStore incorrectly set")
	}
	if got.reverseMapping == nil {
		t.Error("NewReportHandler().reverseMapping = nil, want non-nil")
	}
}

func TestHomeHandler_workspaceOrgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		teamID   string
		setup    func(*testGitHubManager, *testConfigManager)
		wantOrgs int
	}{
		{
			name:   "multiple orgs same workspace",
			teamID: "T123",
			setup: func(gh *testGitHubManager, cm *testConfigManager) {
				gh.addOrg("org1", nil)
				gh.addOrg("org2", nil)
				gh.addOrg("org3", nil)

				cfg1 := &config.RepoConfig{}
				cfg1.Global.TeamID = "T123"
				cm.setConfig("org1", cfg1)

				cfg2 := &config.RepoConfig{}
				cfg2.Global.TeamID = "T123"
				cm.setConfig("org2", cfg2)

				cfg3 := &config.RepoConfig{}
				cfg3.Global.TeamID = "T456"
				cm.setConfig("org3", cfg3)
			},
			wantOrgs: 2,
		},
		{
			name:   "no matching orgs",
			teamID: "T999",
			setup: func(gh *testGitHubManager, cm *testConfigManager) {
				gh.addOrg("org1", nil)

				cfg1 := &config.RepoConfig{}
				cfg1.Global.TeamID = "T123"
				cm.setConfig("org1", cfg1)
			},
			wantOrgs: 0,
		},
		{
			name:   "empty configuration",
			teamID: "T123",
			setup: func(gh *testGitHubManager, cm *testConfigManager) {
				// No setup
			},
			wantOrgs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			githubManager := newTestGitHubManager()
			configManager := newTestConfigManager()

			if tt.setup != nil {
				tt.setup(githubManager, configManager)
			}

			handler := &HomeHandler{
				githubManager: githubManager,
				configManager: configManager,
			}

			got := handler.workspaceOrgs(tt.teamID)

			if len(got) != tt.wantOrgs {
				t.Errorf("workspaceOrgs(%q) returned %d orgs, want %d", tt.teamID, len(got), tt.wantOrgs)
			}
		})
	}
}

func TestHomeHandler_publishPlaceholderHome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userID    string
		setupMock func(*mockAPI)
		wantErr   bool
	}{
		{
			name:   "success with timezone",
			userID: "U123",
			setupMock: func(m *mockAPI) {
				m.getUserInfoFunc = func(ctx context.Context, userID string) (*slack.User, error) {
					return &slack.User{
						ID:       userID,
						TZ:       "America/Los_Angeles",
						TZOffset: -28800,
					}, nil
				}
				m.publishViewFunc = func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
					return &slack.ViewResponse{}, nil
				}
			},
			wantErr: false,
		},
		{
			name:   "timezone error defaults to UTC",
			userID: "U456",
			setupMock: func(m *mockAPI) {
				m.getUserInfoFunc = func(ctx context.Context, userID string) (*slack.User, error) {
					return nil, errors.New("failed to get user info")
				}
				m.publishViewFunc = func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
					return &slack.ViewResponse{}, nil
				}
			},
			wantErr: false,
		},
		{
			name:   "publish view error",
			userID: "U789",
			setupMock: func(m *mockAPI) {
				m.getUserInfoFunc = func(ctx context.Context, userID string) (*slack.User, error) {
					return &slack.User{ID: userID, TZ: "UTC"}, nil
				}
				m.publishViewFunc = func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
					return nil, errors.New("publish failed")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockAPI := &mockAPI{}
			if tt.setupMock != nil {
				tt.setupMock(mockAPI)
			}

			client := &Client{
				api: mockAPI,
				cache: &apiCache{
					entries: make(map[string]cacheEntry),
				},
			}

			handler := &HomeHandler{}

			err := handler.publishPlaceholderHome(
				context.Background(),
				client,
				tt.userID,
				[]string{"org1"},
				nil,
			)

			if (err != nil) != tt.wantErr {
				t.Errorf("publishPlaceholderHome(%q) error = %v, wantErr = %v", tt.userID, err, tt.wantErr)
			}
		})
	}
}

func TestHomeHandler_publishPlaceholderHome_withMapping(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
			return &slack.User{ID: userID, TZ: "UTC"}, nil
		},
		publishViewFunc: func(ctx context.Context, request slack.PublishViewContextRequest) (*slack.ViewResponse, error) {
			return &slack.ViewResponse{}, nil
		},
	}

	client := &Client{
		api: mockAPI,
		cache: &apiCache{
			entries: make(map[string]cacheEntry),
		},
	}

	handler := &HomeHandler{}

	mapping := &usermapping.ReverseMapping{
		GitHubUsername: "testuser",
		MatchMethod:    "email",
		Confidence:     90,
	}

	err := handler.publishPlaceholderHome(
		context.Background(),
		client,
		"U123",
		[]string{"org1"},
		mapping,
	)

	if err != nil {
		t.Errorf("publishPlaceholderHome(U123) with mapping = %v, want nil", err)
	}
}

func TestHomeHandler_HandleAppHomeOpened_clientError(t *testing.T) {
	t.Parallel()

	slackManager := NewManager("test-secret")
	// Don't register any clients - will cause error

	handler := &HomeHandler{
		slackManager:   slackManager,
		githubManager:  newTestGitHubManager(),
		configManager:  newTestConfigManager(),
		reverseMapping: newTestUserMapper(),
	}

	err := handler.HandleAppHomeOpened(context.Background(), "T999", "U123")

	if err == nil {
		t.Error("HandleAppHomeOpened(T999, U123) = nil, want error when client doesn't exist")
	}
}

func TestReportHandler_HandleReportCommand_clientError(t *testing.T) {
	t.Parallel()

	slackManager := NewManager("test-secret")
	// Don't register any clients - will cause error

	handler := &ReportHandler{
		slackManager: slackManager,
	}

	err := handler.HandleReportCommand(context.Background(), "T999", "U123")

	if err == nil {
		t.Error("HandleReportCommand(T999, U123) = nil, want error when client doesn't exist")
	}
}

func TestReportHandler_HandleReportCommand_workspaceInfoError(t *testing.T) {
	t.Parallel()

	mockAPI := &mockAPI{
		getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
			return nil, errors.New("failed to get team info")
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
		slackManager: slackManager,
	}

	err := handler.HandleReportCommand(context.Background(), "T123", "U123")

	if err == nil {
		t.Error("HandleReportCommand(T123, U123) = nil, want error when workspace info fails")
	}
}

func TestReportHandler_HandleReportCommand_noGitHubUsername(t *testing.T) {
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

	userMapper := newTestUserMapper()
	userMapper.setLookupFunc(func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error) {
		return nil, errors.New("user not found")
	})

	handler := &ReportHandler{
		slackManager:   slackManager,
		githubManager:  githubManager,
		reverseMapping: userMapper,
	}

	err := handler.HandleReportCommand(context.Background(), "T123", "U123")

	if err == nil {
		t.Error("HandleReportCommand(T123, U123) = nil, want error when GitHub username not found")
	}
}
