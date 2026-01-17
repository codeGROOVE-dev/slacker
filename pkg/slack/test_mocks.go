package slack

import (
	"context"
	"sync"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
)

// testGitHubManager is a mock implementation of GitHubManager for testing.
type testGitHubManager struct {
	mu      sync.RWMutex
	orgs    []string
	clients map[string]*github.Client
}

func newTestGitHubManager() *testGitHubManager {
	return &testGitHubManager{
		orgs:    []string{},
		clients: make(map[string]*github.Client),
	}
}

func (m *testGitHubManager) AllOrgs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, len(m.orgs))
	copy(result, m.orgs)
	return result
}

func (m *testGitHubManager) ClientForOrg(org string) (*github.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, ok := m.clients[org]
	return client, ok
}

func (m *testGitHubManager) addOrg(org string, client *github.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orgs = append(m.orgs, org)
	m.clients[org] = client
}

// testConfigManager is a mock implementation of ConfigManager for testing.
type testConfigManager struct {
	mu      sync.RWMutex
	configs map[string]*config.RepoConfig
}

func newTestConfigManager() *testConfigManager {
	return &testConfigManager{
		configs: make(map[string]*config.RepoConfig),
	}
}

func (m *testConfigManager) Config(org string) (*config.RepoConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.configs[org]
	return cfg, ok
}

func (m *testConfigManager) setConfig(org string, cfg *config.RepoConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[org] = cfg
}

// testUserMapper is a mock implementation of UserMapper for testing.
type testUserMapper struct {
	mu               sync.RWMutex
	lookupFunc       func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error)
	setOverridesFunc func(overrides map[string]string)
	overrides        map[string]string
}

func newTestUserMapper() *testUserMapper {
	return &testUserMapper{
		overrides: make(map[string]string),
	}
}

func (m *testUserMapper) LookupGitHub(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.lookupFunc != nil {
		return m.lookupFunc(ctx, slackAPI, slackUserID, org, emailDomain)
	}
	return nil, nil
}

func (m *testUserMapper) SetOverrides(overrides map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range overrides {
		m.overrides[k] = v
	}
	if m.setOverridesFunc != nil {
		m.setOverridesFunc(overrides)
	}
}

func (m *testUserMapper) setLookupFunc(f func(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookupFunc = f
}
