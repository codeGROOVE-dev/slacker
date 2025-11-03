package bot

import (
	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	"github.com/slack-go/slack"
)

// MockSlackBuilder provides a fluent API for building mockSlackClient instances.
// This makes test setup much more readable and maintainable.
//
// Example:
//
//	mockSlack := NewMockSlack().
//		WithChannelResolution("testrepo", "C123").
//		WithPostThreadSuccess("1234.567").
//		Build()
type MockSlackBuilder struct {
	mock *mockSlackClient
}

// NewMockSlack creates a new mock Slack client builder with sensible defaults.
func NewMockSlack() *MockSlackBuilder {
	return &MockSlackBuilder{
		mock: &mockSlackClient{
			postedMessages:   []mockPostedMessage{},
			updatedMessages:  []mockUpdatedMessage{},
			updatedDMMessage: []mockUpdatedDMMessage{},
		},
	}
}

// WithChannelResolution configures the mock to resolve a channel name to a specific ID.
func (b *MockSlackBuilder) WithChannelResolution(channelName, channelID string) *MockSlackBuilder {
	b.mock.resolveChannelFunc = func(ctx context.Context, name string) string {
		if name == channelName {
			return channelID
		}
		return ""
	}
	return b
}

// WithChannelResolutionMap configures the mock to resolve multiple channel names.
func (b *MockSlackBuilder) WithChannelResolutionMap(channels map[string]string) *MockSlackBuilder {
	b.mock.resolveChannelFunc = func(ctx context.Context, name string) string {
		if id, ok := channels[name]; ok {
			return id
		}
		return ""
	}
	return b
}

// WithPostThreadSuccess configures the mock to successfully post threads with a given timestamp.
func (b *MockSlackBuilder) WithPostThreadSuccess(timestamp string) *MockSlackBuilder {
	b.mock.postThreadFunc = func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
		return timestamp, nil
	}
	return b
}

// WithPostThreadError configures the mock to fail when posting threads.
func (b *MockSlackBuilder) WithPostThreadError(err error) *MockSlackBuilder {
	b.mock.postThreadFunc = func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
		return "", err
	}
	return b
}

// WithUpdateMessageSuccess configures the mock to successfully update messages.
func (b *MockSlackBuilder) WithUpdateMessageSuccess() *MockSlackBuilder {
	b.mock.updateMessageFunc = func(ctx context.Context, channelID, timestamp, text string) error {
		return nil
	}
	return b
}

// WithUpdateMessageError configures the mock to fail when updating messages.
func (b *MockSlackBuilder) WithUpdateMessageError(err error) *MockSlackBuilder {
	b.mock.updateMessageFunc = func(ctx context.Context, channelID, timestamp, text string) error {
		return err
	}
	return b
}

// WithBotInChannel configures whether the bot is in a channel.
func (b *MockSlackBuilder) WithBotInChannel(inChannel bool) *MockSlackBuilder {
	b.mock.botInChannelFunc = func(ctx context.Context, channelID string) bool {
		return inChannel
	}
	return b
}

// WithWorkspaceInfo configures the workspace info returned by the mock.
func (b *MockSlackBuilder) WithWorkspaceInfo(domain string) *MockSlackBuilder {
	b.mock.workspaceInfo = &slack.TeamInfo{
		Domain: domain,
	}
	return b
}

// WithWorkspaceInfoError configures the mock to fail when retrieving workspace info.
func (b *MockSlackBuilder) WithWorkspaceInfoError() *MockSlackBuilder {
	b.mock.workspaceInfoErr = true
	return b
}

// Build returns the configured mockSlackClient.
func (b *MockSlackBuilder) Build() *mockSlackClient {
	return b.mock
}

// MockStateBuilder provides a fluent API for building mockStateStore instances.
type MockStateBuilder struct {
	mock *mockStateStore
}

// NewMockState creates a new mock state store builder with sensible defaults.
func NewMockState() *MockStateBuilder {
	return &MockStateBuilder{
		mock: &mockStateStore{
			threads:           make(map[string]ThreadInfo),
			dmTimes:           make(map[string]time.Time),
			dmUsers:           make(map[string][]string),
			processedEvents:   make(map[string]bool),
			lastNotifications: make(map[string]time.Time),
		},
	}
}

// WithThread pre-populates a thread in the state store.
func (b *MockStateBuilder) WithThread(owner, repo string, number int, channelID string, info ThreadInfo) *MockStateBuilder {
	key := fmt.Sprintf("%s/%s#%d:%s", owner, repo, number, channelID)
	b.mock.threads[key] = info
	return b
}

// WithProcessedEvent marks an event as already processed.
func (b *MockStateBuilder) WithProcessedEvent(eventKey string) *MockStateBuilder {
	b.mock.processedEvents[eventKey] = true
	return b
}

// WithMarkProcessedError configures the mock to fail when marking events as processed.
func (b *MockStateBuilder) WithMarkProcessedError(err error) *MockStateBuilder {
	b.mock.markProcessedErr = err
	return b
}

// WithSaveThreadError configures the mock to fail when saving threads.
func (b *MockStateBuilder) WithSaveThreadError(err error) *MockStateBuilder {
	b.mock.saveThreadErr = err
	return b
}

// Build returns the configured mockStateStore.
func (b *MockStateBuilder) Build() *mockStateStore {
	return b.mock
}

// MockUserMapperBuilder provides a fluent API for building mockUserMapper instances.
type MockUserMapperBuilder struct {
	mock *mockUserMapper
}

// NewMockUserMapper creates a new mock user mapper builder.
func NewMockUserMapper() *MockUserMapperBuilder {
	return &MockUserMapperBuilder{
		mock: &mockUserMapper{},
	}
}

// WithGitHubToSlackMapping configures a simple GitHub username to Slack ID mapping.
func (b *MockUserMapperBuilder) WithGitHubToSlackMapping(githubUser, slackID string) *MockUserMapperBuilder {
	b.mock.slackHandleFunc = func(ctx context.Context, user, org, domain string) (string, error) {
		if user == githubUser {
			return slackID, nil
		}
		return "", errors.New("user not found")
	}
	return b
}

// WithMappings configures multiple GitHub username to Slack ID mappings.
func (b *MockUserMapperBuilder) WithMappings(mappings map[string]string) *MockUserMapperBuilder {
	b.mock.slackHandleFunc = func(ctx context.Context, user, org, domain string) (string, error) {
		if slackID, ok := mappings[user]; ok {
			return slackID, nil
		}
		return "", errors.New("user not found")
	}
	return b
}

// WithDefaultMapping configures a default mapping that prefixes GitHub usernames with "U".
func (b *MockUserMapperBuilder) WithDefaultMapping() *MockUserMapperBuilder {
	b.mock.slackHandleFunc = func(ctx context.Context, user, org, domain string) (string, error) {
		if user == "_system" {
			return "", nil
		}
		return "U" + user, nil
	}
	return b
}

// Build returns the configured mockUserMapper.
func (b *MockUserMapperBuilder) Build() *mockUserMapper {
	return b.mock
}

// CoordinatorBuilder provides a fluent API for building Coordinator instances for tests.
type CoordinatorBuilder struct {
	coordinator *Coordinator
}

// NewTestCoordinator creates a new Coordinator builder with sensible defaults for testing.
func NewTestCoordinator() *CoordinatorBuilder {
	return &CoordinatorBuilder{
		coordinator: &Coordinator{
			slack:          NewMockSlack().Build(),
			github:         &mockGitHub{org: "testorg", token: "test-token"},
			stateStore:     NewMockState().Build(),
			configManager:  config.New(),
			commitPRCache:  cache.NewCommitPRCache(),
			threadCache:    cache.New(),
			eventSemaphore: make(chan struct{}, 10),
			workspaceName:  "test-workspace.slack.com",
		},
	}
}

// WithSlack configures the Slack client.
func (b *CoordinatorBuilder) WithSlack(slack *mockSlackClient) *CoordinatorBuilder {
	b.coordinator.slack = slack
	return b
}

// WithGitHub configures the GitHub client.
func (b *CoordinatorBuilder) WithGitHub(github *mockGitHub) *CoordinatorBuilder {
	b.coordinator.github = github
	return b
}

// WithState configures the state store.
func (b *CoordinatorBuilder) WithState(state *mockStateStore) *CoordinatorBuilder {
	b.coordinator.stateStore = state
	return b
}

// WithConfig configures the config manager.
func (b *CoordinatorBuilder) WithConfig(cfg *config.Manager) *CoordinatorBuilder {
	b.coordinator.configManager = cfg
	return b
}

// WithNotifier configures the notifier.
func (b *CoordinatorBuilder) WithNotifier(notifier *notify.Manager) *CoordinatorBuilder {
	b.coordinator.notifier = notifier
	return b
}

// WithUserMapper configures the user mapper.
func (b *CoordinatorBuilder) WithUserMapper(mapper UserMapper) *CoordinatorBuilder {
	b.coordinator.userMapper = mapper
	return b
}

// WithWorkspaceName configures the workspace name.
func (b *CoordinatorBuilder) WithWorkspaceName(name string) *CoordinatorBuilder {
	b.coordinator.workspaceName = name
	return b
}

// Build returns the configured Coordinator.
func (b *CoordinatorBuilder) Build() *Coordinator {
	return b.coordinator
}
