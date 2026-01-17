package bot

import (
	"context"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	gogithub "github.com/google/go-github/v50/github"
)

// TestCheckDailyReports_NoConfig tests when config doesn't exist for org
func TestCheckDailyReports_NoConfig(t *testing.T) {
	ctx := context.Background()

	mockConfig := NewMockConfig().Build()
	// Don't add any config data - Config() will return false

	c := &Coordinator{
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	// Should return early without error
	c.checkDailyReports(ctx, "unknown-org", []github.PRSnapshot{})

	// Test passes if no panic occurs
}

// TestCheckDailyReports_DailyReportsDisabled tests when daily reports are disabled
func TestCheckDailyReports_DailyReportsDisabled(t *testing.T) {
	ctx := context.Background()

	// Create config with daily reports disabled
	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = true
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	c := &Coordinator{
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	prs := []github.PRSnapshot{
		{Author: "user1", Number: 1},
	}

	c.checkDailyReports(ctx, "test-org", prs)

	// Test passes if no panic occurs
}

// TestCheckDailyReports_NoUsers tests when no users are extracted from PRs
func TestCheckDailyReports_NoUsers(t *testing.T) {
	ctx := context.Background()

	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = false
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	c := &Coordinator{
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	// Empty PR list - no users to extract
	c.checkDailyReports(ctx, "test-org", []github.PRSnapshot{})

	// Test passes if no panic occurs
}

// TestCheckDailyReports_NoGitHubToken tests when GitHub token is empty
func TestCheckDailyReports_NoGitHubToken(t *testing.T) {
	ctx := context.Background()

	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = false
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	// Mock GitHub client that returns empty token
	mockGH := &mockGitHubClientWithToken{
		token: "",
	}

	c := &Coordinator{
		configManager: mockConfig,
		github:        mockGH,
		threadCache:   cache.New(),
	}

	prs := []github.PRSnapshot{
		{Author: "user1", Number: 1},
	}

	c.checkDailyReports(ctx, "test-org", prs)

	// Test passes if no panic occurs
}

// TestCheckDailyReports_InvalidGitHubClient tests when GitHub client type assertion fails
func TestCheckDailyReports_InvalidGitHubClient(t *testing.T) {
	ctx := context.Background()

	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = false
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	// Mock GitHub client that returns token but wrong client type
	mockGH := &mockGitHubClientWithToken{
		token:      "test-token",
		clientType: "wrong-type", // Not *gogithub.Client
	}

	c := &Coordinator{
		configManager: mockConfig,
		github:        mockGH,
		threadCache:   cache.New(),
	}

	prs := []github.PRSnapshot{
		{Author: "user1", Number: 1},
	}

	c.checkDailyReports(ctx, "test-org", prs)

	// Test passes if no panic occurs
}

// TestCheckDailyReports_UserMapperFailure tests when user mapping fails
func TestCheckDailyReports_UserMapperFailure(t *testing.T) {
	ctx := context.Background()

	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = false
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	mockGH := &mockGitHubClientWithToken{
		token:      "test-token",
		clientType: &gogithub.Client{},
	}

	// Mock user mapper that always fails
	mockMapper := &mockUserMapper{
		failLookups: true,
	}

	c := &Coordinator{
		configManager: mockConfig,
		github:        mockGH,
		userMapper:    mockMapper,
		stateStore:    state.NewMemoryStore(),
		slack:         NewMockSlack().Build(),
		threadCache:   cache.New(),
	}

	prs := []github.PRSnapshot{
		{Author: "user1", Number: 1},
	}

	c.checkDailyReports(ctx, "test-org", prs)

	// Test passes if no panic occurs
}

// TestCheckDailyReports_EmptySlackUserID tests when user mapping returns empty string
func TestCheckDailyReports_EmptySlackUserID(t *testing.T) {
	ctx := context.Background()

	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = false
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	mockGH := &mockGitHubClientWithToken{
		token:      "test-token",
		clientType: &gogithub.Client{},
	}

	// Mock user mapper that returns empty string
	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "", // Empty Slack ID
		},
	}

	c := &Coordinator{
		configManager: mockConfig,
		github:        mockGH,
		userMapper:    mockMapper,
		stateStore:    state.NewMemoryStore(),
		slack:         NewMockSlack().Build(),
		threadCache:   cache.New(),
	}

	prs := []github.PRSnapshot{
		{Author: "user1", Number: 1},
	}

	c.checkDailyReports(ctx, "test-org", prs)

	// Test passes if no panic occurs and user is skipped
}

// mockGitHubClientWithToken is a mock GitHub client for daily reports testing
type mockGitHubClientWithToken struct {
	token      string
	clientType interface{}
}

func (m *mockGitHubClientWithToken) InstallationToken(ctx context.Context) string {
	return m.token
}

func (m *mockGitHubClientWithToken) Organization() string {
	return "test-org"
}

func (m *mockGitHubClientWithToken) Client() any {
	if m.clientType != nil {
		return m.clientType
	}
	return nil
}

func (m *mockGitHubClientWithToken) FindPRsForCommit(ctx context.Context, owner, repo, sha string) ([]int, error) {
	return nil, nil
}

func (m *mockGitHubClientWithToken) RefreshToken(ctx context.Context) error {
	return nil
}

// TestCheckDailyReports_DashboardFetchFailure tests when dashboard fetch fails
func TestCheckDailyReports_DashboardFetchFailure(t *testing.T) {
	ctx := context.Background()

	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = false
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	// Use real gogithub.Client that will fail API calls
	ghClient := gogithub.NewClient(nil)

	mockGH := &mockGitHubClientWithToken{
		token:      "test-token",
		clientType: ghClient,
	}

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := &Coordinator{
		configManager: mockConfig,
		github:        mockGH,
		userMapper:    mockMapper,
		stateStore:    state.NewMemoryStore(),
		slack:         NewMockSlack().Build(),
		threadCache:   cache.New(),
	}

	prs := []github.PRSnapshot{
		{Author: "user1", Number: 1},
	}

	// This should fail when trying to fetch dashboard but handle the error gracefully
	c.checkDailyReports(ctx, "test-org", prs)

	// Test passes if no panic occurs
}

// TestCheckDailyReports_ContextCanceled tests context cancellation during rate limiting
func TestCheckDailyReports_ContextCanceled(t *testing.T) {
	// Create a context that's already canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	cfg := &config.RepoConfig{}
	cfg.Global.DisableDailyReport = false
	cfg.Global.EmailDomain = "example.com"

	mockConfig := NewMockConfig().Build()
	mockConfig.configData = map[string]interface{}{
		"test-org": cfg,
	}

	ghClient := gogithub.NewClient(nil)

	mockGH := &mockGitHubClientWithToken{
		token:      "test-token",
		clientType: ghClient,
	}

	mockMapper := &mockUserMapper{
		mapping: map[string]string{
			"user1": "U123",
		},
	}

	c := &Coordinator{
		configManager: mockConfig,
		github:        mockGH,
		userMapper:    mockMapper,
		stateStore:    state.NewMemoryStore(),
		slack:         NewMockSlack().Build(),
		threadCache:   cache.New(),
	}

	prs := []github.PRSnapshot{
		{Author: "user1", Number: 1},
	}

	// Should handle canceled context gracefully
	c.checkDailyReports(ctx, "test-org", prs)

	// Test passes if no panic occurs
}
