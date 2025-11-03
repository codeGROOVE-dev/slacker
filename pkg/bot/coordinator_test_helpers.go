package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

	ghmailto "github.com/codeGROOVE-dev/gh-mailto/pkg/gh-mailto"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
	"github.com/slack-go/slack"
)

// mockStateStore implements StateStore interface from bot package.
type mockStateStore struct {
	threads           map[string]ThreadInfo
	dmTimes           map[string]time.Time
	dmUsers           map[string][]string
	processedEvents   map[string]bool
	lastNotifications map[string]time.Time
	markProcessedErr  error // Error to return from MarkProcessed
	saveThreadErr     error // Error to return from SaveThread
}

func (m *mockStateStore) Thread(owner, repo string, number int, channelID string) (ThreadInfo, bool) {
	key := fmt.Sprintf("%s/%s#%d:%s", owner, repo, number, channelID)
	if m.threads != nil {
		if info, ok := m.threads[key]; ok {
			return info, true
		}
	}
	return ThreadInfo{}, false
}

func (m *mockStateStore) SaveThread(owner, repo string, number int, channelID string, info ThreadInfo) error {
	if m.saveThreadErr != nil {
		return m.saveThreadErr
	}
	key := fmt.Sprintf("thread:%s/%s#%d:%s", owner, repo, number, channelID)
	if m.threads == nil {
		m.threads = make(map[string]ThreadInfo)
	}
	m.threads[key] = info
	return nil
}

func (m *mockStateStore) LastDM(userID, prURL string) (time.Time, bool) {
	key := userID + ":" + prURL
	if m.dmTimes != nil {
		if t, ok := m.dmTimes[key]; ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func (m *mockStateStore) RecordDM(userID, prURL string, sentAt time.Time) error {
	key := userID + ":" + prURL
	if m.dmTimes == nil {
		m.dmTimes = make(map[string]time.Time)
	}
	m.dmTimes[key] = sentAt
	return nil
}

func (m *mockStateStore) ListDMUsers(prURL string) []string {
	if m.dmUsers != nil {
		if users, ok := m.dmUsers[prURL]; ok {
			return users
		}
	}
	return []string{}
}

func (m *mockStateStore) WasProcessed(eventKey string) bool {
	if m.processedEvents != nil {
		return m.processedEvents[eventKey]
	}
	return false
}

func (m *mockStateStore) MarkProcessed(eventKey string, _ time.Duration) error {
	if m.markProcessedErr != nil {
		return m.markProcessedErr
	}
	if m.processedEvents == nil {
		m.processedEvents = make(map[string]bool)
	}
	m.processedEvents[eventKey] = true
	return nil
}

func (m *mockStateStore) LastNotification(prURL string) time.Time {
	if m.lastNotifications != nil {
		if t, ok := m.lastNotifications[prURL]; ok {
			return t
		}
	}
	return time.Time{}
}

func (m *mockStateStore) RecordNotification(prURL string, notifiedAt time.Time) error {
	if m.lastNotifications == nil {
		m.lastNotifications = make(map[string]time.Time)
	}
	m.lastNotifications[prURL] = notifiedAt
	return nil
}

// notify.Store interface methods for DM queue management.
func (*mockStateStore) QueuePendingDM(dm state.PendingDM) error {
	return nil // No-op for tests
}

func (*mockStateStore) GetPendingDMs(before time.Time) ([]state.PendingDM, error) {
	return nil, nil // Return empty list for tests
}

func (*mockStateStore) RemovePendingDM(id string) error {
	return nil // No-op for tests
}

func (*mockStateStore) Close() error {
	return nil
}

// mockSlackClient implements SlackClient for testing.
//
//nolint:govet // fieldalignment optimization would reduce test readability
type mockSlackClient struct {
	postThreadFunc      func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error)
	updateMessageFunc   func(ctx context.Context, channelID, timestamp, text string) error
	updateDMMessageFunc func(ctx context.Context, userID, timestamp, text string) error
	channelHistoryFunc  func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error)
	resolveChannelFunc  func(ctx context.Context, channelName string) string
	botInChannelFunc    func(ctx context.Context, channelID string) bool
	botInfoFunc         func(ctx context.Context) (*slack.AuthTestResponse, error)
	workspaceInfoFunc   func(ctx context.Context) (*slack.TeamInfo, error)
	publishHomeFunc     func(ctx context.Context, userID string, blocks []slack.Block) error
	apiFunc             func() *slack.Client

	// For direct workspace info control
	workspaceInfo    *slack.TeamInfo
	workspaceInfoErr bool

	// Tracking for test assertions
	postedMessages   []mockPostedMessage
	updatedMessages  []mockUpdatedMessage
	updatedDMMessage []mockUpdatedDMMessage
}

type mockPostedMessage struct {
	ChannelID   string
	Text        string
	Attachments []slack.Attachment
}

type mockUpdatedMessage struct {
	ChannelID string
	Timestamp string
	Text      string
}

type mockUpdatedDMMessage struct {
	UserID string
	PRURL  string
	Text   string
}

func (m *mockSlackClient) PostThread(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
	m.postedMessages = append(m.postedMessages, mockPostedMessage{
		ChannelID:   channelID,
		Text:        text,
		Attachments: attachments,
	})
	if m.postThreadFunc != nil {
		return m.postThreadFunc(ctx, channelID, text, attachments)
	}
	return "1234567890.123456", nil
}

func (m *mockSlackClient) UpdateMessage(ctx context.Context, channelID, timestamp, text string) error {
	m.updatedMessages = append(m.updatedMessages, mockUpdatedMessage{
		ChannelID: channelID,
		Timestamp: timestamp,
		Text:      text,
	})
	if m.updateMessageFunc != nil {
		return m.updateMessageFunc(ctx, channelID, timestamp, text)
	}
	return nil
}

func (m *mockSlackClient) UpdateDMMessage(ctx context.Context, userID, prURL, text string) error {
	m.updatedDMMessage = append(m.updatedDMMessage, mockUpdatedDMMessage{
		UserID: userID,
		PRURL:  prURL,
		Text:   text,
	})
	if m.updateDMMessageFunc != nil {
		return m.updateDMMessageFunc(ctx, userID, prURL, text)
	}
	return nil
}

//nolint:revive // line length acceptable for interface signature
func (m *mockSlackClient) ChannelHistory(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error) {
	if m.channelHistoryFunc != nil {
		return m.channelHistoryFunc(ctx, channelID, oldest, latest, limit)
	}
	return &slack.GetConversationHistoryResponse{}, nil
}

func (m *mockSlackClient) ResolveChannelID(ctx context.Context, channelName string) string {
	if m.resolveChannelFunc != nil {
		return m.resolveChannelFunc(ctx, channelName)
	}
	return "C123"
}

func (m *mockSlackClient) IsBotInChannel(ctx context.Context, channelID string) bool {
	if m.botInChannelFunc != nil {
		return m.botInChannelFunc(ctx, channelID)
	}
	return true
}

func (m *mockSlackClient) BotInfo(ctx context.Context) (*slack.AuthTestResponse, error) {
	if m.botInfoFunc != nil {
		return m.botInfoFunc(ctx)
	}
	return &slack.AuthTestResponse{UserID: "B123"}, nil
}

func (m *mockSlackClient) WorkspaceInfo(ctx context.Context) (*slack.TeamInfo, error) {
	if m.workspaceInfoFunc != nil {
		return m.workspaceInfoFunc(ctx)
	}
	if m.workspaceInfoErr {
		return nil, errors.New("workspace info error")
	}
	if m.workspaceInfo != nil {
		return m.workspaceInfo, nil
	}
	return &slack.TeamInfo{}, nil
}

func (m *mockSlackClient) PublishHomeView(ctx context.Context, userID string, blocks []slack.Block) error {
	if m.publishHomeFunc != nil {
		return m.publishHomeFunc(ctx, userID, blocks)
	}
	return nil
}

func (m *mockSlackClient) API() *slack.Client {
	if m.apiFunc != nil {
		return m.apiFunc()
	}
	return nil
}

// newMockUserMapper creates a usermapping.Service for testing.
// Since we can't inject mocks into private fields, we use a real Service with nil Slack client.
// The tests won't call methods that need the Slack client.
func newMockUserMapper(_ *mockSlackClient) *usermapping.Service {
	return usermapping.New(nil, "test-token")
}

// mockSlackAPIForUserMapping implements usermapping.SlackAPI interface.
type mockSlackAPIForUserMapping struct{}

func (*mockSlackAPIForUserMapping) GetUserByEmailContext(ctx context.Context, email string) (*slack.User, error) {
	// Return a mock user for any email
	return &slack.User{
		ID:   "U" + email[:min(len(email), 5)],
		Name: "testuser",
		Profile: slack.UserProfile{
			Email: email,
		},
	}, nil
}

func (*mockSlackAPIForUserMapping) GetUserInfo(userID string) (*slack.User, error) {
	return &slack.User{
		ID:   userID,
		Name: "testuser",
	}, nil
}

// mockGitHubEmailLookup implements usermapping.GitHubEmailLookup interface.
type mockGitHubEmailLookup struct{}

func (*mockGitHubEmailLookup) Lookup(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
	// Return a mock result with a test email
	return &ghmailto.Result{
		Addresses: []ghmailto.Address{
			{
				Email:   username + "@test.com",
				Methods: []string{"mock"},
			},
		},
	}, nil
}

func (*mockGitHubEmailLookup) Guess(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error) {
	return &ghmailto.GuessResult{
		Username:       username,
		Guesses:        []ghmailto.Address{},
		FoundAddresses: []ghmailto.Address{},
	}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// mockUserMapper is a simple mock for user mapping in tests.
type mockUserMapper struct {
	slackHandleFunc func(ctx context.Context, githubUser, org, domain string) (string, error)
	mapping         map[string]string // GitHub username -> Slack user ID
	failLookups     bool              // If true, all lookups fail
}

func (m *mockUserMapper) SlackHandle(ctx context.Context, githubUser, org, domain string) (string, error) {
	if m.slackHandleFunc != nil {
		return m.slackHandleFunc(ctx, githubUser, org, domain)
	}
	if m.failLookups {
		return "", errors.New("user mapping failed")
	}
	if m.mapping != nil {
		if slackID, ok := m.mapping[githubUser]; ok {
			return slackID, nil
		}
		return "", nil // Not found in mapping
	}
	// Default: return a simple mock Slack user ID based on GitHub username
	if githubUser == "_system" {
		return "", nil // Skip _system
	}
	return "U" + githubUser, nil
}

func (m *mockUserMapper) FormatUserMentions(ctx context.Context, githubUsers []string, owner, domain string) string {
	mentions := ""
	for i, user := range githubUsers {
		slackID, _ := m.SlackHandle(ctx, user, owner, domain)
		if slackID == "" {
			continue
		}
		if i > 0 && mentions != "" {
			mentions += ", "
		}
		mentions += "<@" + slackID + ">"
	}
	return mentions
}

// mockTracker is a simple mock for notification tracking in tests.
type mockTracker struct {
	channelNotified bool
	userTags        []mockUserTag
	tagInfoByUser   map[string]TagInfo // Map from slackUserID to TagInfo for testing
}

type mockUserTag struct {
	workspaceID string
	slackUserID string
	channelID   string
	owner       string
	repo        string
	prNumber    int
}

func (m *mockTracker) UpdateChannelNotification(workspaceID, owner, repo string, prNumber int) {
	m.channelNotified = true
}

func (m *mockTracker) UpdateUserPRChannelTag(workspaceID, slackUserID, channelID, owner, repo string, prNumber int) {
	m.userTags = append(m.userTags, mockUserTag{
		workspaceID: workspaceID,
		slackUserID: slackUserID,
		channelID:   channelID,
		owner:       owner,
		repo:        repo,
		prNumber:    prNumber,
	})
}

func (m *mockTracker) LastUserPRChannelTag(workspaceID, slackUserID, owner, repo string, prNumber int) TagInfo {
	if m.tagInfoByUser != nil {
		if tagInfo, ok := m.tagInfoByUser[slackUserID]; ok {
			return tagInfo
		}
	}
	return TagInfo{}
}

// mockNotifier is a simple mock for notification manager in tests.
type mockNotifier struct {
	Tracker         *mockTracker
	notifyUserError error
	notifyCalls     []notifyUserCall
}

type notifyUserCall struct {
	workspaceID string
	userID      string
	channelID   string
	channelName string
}

// NotifyUser mocks the notify.Manager.NotifyUser method.
func (m *mockNotifier) NotifyUser(ctx context.Context, workspaceID, userID, channelID, channelName string, pr interface{}) error {
	m.notifyCalls = append(m.notifyCalls, notifyUserCall{
		workspaceID: workspaceID,
		userID:      userID,
		channelID:   channelID,
		channelName: channelName,
	})
	return m.notifyUserError
}

// TagInfo matches the one in pkg/notify for test compatibility.
type TagInfo struct {
	ChannelID   string
	TaggedAt    time.Time
	WorkspaceID string
}

// notifyError is a simple error type for testing notification failures.
type notifyError struct {
	message string
}

func (e *notifyError) Error() string {
	return e.message
}

// mockPRSearcher implements PRSearcher interface for testing polling logic.
type mockPRSearcher struct {
	listOpenPRsFunc   func(ctx context.Context, org string, updatedSinceHours int) ([]github.PRSnapshot, error)
	listClosedPRsFunc func(ctx context.Context, org string, updatedSinceHours int) ([]github.PRSnapshot, error)
}

func (m *mockPRSearcher) ListOpenPRs(ctx context.Context, org string, updatedSinceHours int) ([]github.PRSnapshot, error) {
	if m.listOpenPRsFunc != nil {
		return m.listOpenPRsFunc(ctx, org, updatedSinceHours)
	}
	return nil, errors.New("mock: ListOpenPRs not configured")
}

func (m *mockPRSearcher) ListClosedPRs(ctx context.Context, org string, updatedSinceHours int) ([]github.PRSnapshot, error) {
	if m.listClosedPRsFunc != nil {
		return m.listClosedPRsFunc(ctx, org, updatedSinceHours)
	}
	return nil, errors.New("mock: ListClosedPRs not configured")
}
