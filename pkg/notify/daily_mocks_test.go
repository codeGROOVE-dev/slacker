package notify

import (
	"context"
	"time"

	"github.com/codeGROOVE-dev/prx/pkg/prx"
	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	gh "github.com/google/go-github/v50/github"
)

// mockGitHubClient mocks github.ClientInterface for testing.
type mockGitHubClient struct {
	installationTokenFunc func(ctx context.Context) string
	clientFunc            func() *gh.Client
}

func (m *mockGitHubClient) InstallationToken(ctx context.Context) string {
	if m.installationTokenFunc != nil {
		return m.installationTokenFunc(ctx)
	}
	return "test-token"
}

func (m *mockGitHubClient) Client() any {
	if m.clientFunc != nil {
		return m.clientFunc()
	}
	return gh.NewClient(nil)
}

// mockGitHubManager mocks github.ManagerInterface for testing.
type mockGitHubManager struct {
	allOrgsFunc      func() []string
	clientForOrgFunc func(org string) (github.ClientInterface, bool)
}

func (m *mockGitHubManager) AllOrgs() []string {
	if m.allOrgsFunc != nil {
		return m.allOrgsFunc()
	}
	return []string{}
}

func (m *mockGitHubManager) ClientForOrg(org string) (github.ClientInterface, bool) {
	if m.clientForOrgFunc != nil {
		return m.clientForOrgFunc(org)
	}
	return nil, false
}

// mockDigestUserMapper mocks DigestUserMapper for testing.
type mockDigestUserMapper struct {
	slackHandleFunc func(ctx context.Context, githubUser, org, domain string) (string, error)
}

func (m *mockDigestUserMapper) SlackHandle(ctx context.Context, githubUser, org, domain string) (string, error) {
	if m.slackHandleFunc != nil {
		return m.slackHandleFunc(ctx, githubUser, org, domain)
	}
	return "", nil
}

// mockTurnClient mocks turn.Client for testing.
type mockTurnClient struct {
	checkFunc func(ctx context.Context, prURL, author string, updatedAt time.Time) (*turn.CheckResponse, error)
}

func (m *mockTurnClient) Check(ctx context.Context, prURL, author string, updatedAt time.Time) (*turn.CheckResponse, error) {
	if m.checkFunc != nil {
		return m.checkFunc(ctx, prURL, author, updatedAt)
	}
	return &turn.CheckResponse{
		PullRequest: prx.PullRequest{},
		Analysis: turn.Analysis{
			NextAction: make(map[string]turn.Action),
		},
	}, nil
}

// mockConfigProvider implements ConfigProvider for daily digest tests.
type mockConfigProvider struct {
	dailyRemindersEnabledFunc func(org string) bool
	domainFunc                func(org string) string
	configFunc                func(org string) (*config.RepoConfig, bool)
	reminderDMDelayFunc       func(org, channel string) int
}

func (m *mockConfigProvider) DailyRemindersEnabled(org string) bool {
	if m.dailyRemindersEnabledFunc != nil {
		return m.dailyRemindersEnabledFunc(org)
	}
	return true
}

func (m *mockConfigProvider) Domain(org string) string {
	if m.domainFunc != nil {
		return m.domainFunc(org)
	}
	return "example.slack.com"
}

func (m *mockConfigProvider) Config(org string) (*config.RepoConfig, bool) {
	if m.configFunc != nil {
		return m.configFunc(org)
	}
	cfg := &config.RepoConfig{}
	cfg.Global.TeamID = "T123"
	return cfg, true
}

func (m *mockConfigProvider) ReminderDMDelay(org, channel string) int {
	if m.reminderDMDelayFunc != nil {
		return m.reminderDMDelayFunc(org, channel)
	}
	return 65 // Default delay
}

// mockStateProvider implements StateProvider for daily digest tests.
type mockStateProvider struct {
	lastDigestFunc   func(userID, date string) (time.Time, bool)
	recordDigestFunc func(userID, date string, sentAt time.Time) error
	lastDMFunc       func(ctx context.Context, userID, prURL string) (time.Time, bool)
}

func (m *mockStateProvider) LastDigest(ctx context.Context, userID, date string) (time.Time, bool) {
	if m.lastDigestFunc != nil {
		return m.lastDigestFunc(userID, date)
	}
	return time.Time{}, false
}

func (m *mockStateProvider) RecordDigest(ctx context.Context, userID, date string, sentAt time.Time) error {
	if m.recordDigestFunc != nil {
		return m.recordDigestFunc(userID, date, sentAt)
	}
	return nil
}

func (m *mockStateProvider) LastDM(ctx context.Context, userID, prURL string) (time.Time, bool) {
	if m.lastDMFunc != nil {
		return m.lastDMFunc(ctx, userID, prURL)
	}
	return time.Time{}, false
}

// createTestCheckResponse creates a test turnclient CheckResponse.
func createTestCheckResponse(blockedUser string, actionKind string) *turn.CheckResponse {
	return &turn.CheckResponse{
		PullRequest: prx.PullRequest{},
		Analysis: turn.Analysis{
			NextAction: map[string]turn.Action{
				blockedUser: {
					Kind:   turn.ActionKind(actionKind),
					Reason: "Test reason",
				},
			},
		},
	}
}
