package bot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	slackapi "github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
	"github.com/slack-go/slack"
)

// mockStateStore implements StateStore interface from bot package.
//
//nolint:govet // fieldalignment optimization would reduce test readability
type mockStateStore struct {
	markProcessedErr  error
	saveThreadErr     error
	saveDMMessageErr  error
	threads           map[string]cache.ThreadInfo
	dmTimes           map[string]time.Time
	dmUsers           map[string][]string
	dmMessages        map[string]state.DMInfo
	pendingDMs        []*state.PendingDM
	processedEvents   map[string]bool
	lastNotifications map[string]time.Time
	mu                sync.Mutex
}

func (m *mockStateStore) Thread(ctx context.Context, owner, repo string, number int, channelID string) (cache.ThreadInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%s#%d:%s", owner, repo, number, channelID)
	if m.threads != nil {
		if info, ok := m.threads[key]; ok {
			return info, true
		}
	}
	return cache.ThreadInfo{}, false
}

func (m *mockStateStore) SaveThread(ctx context.Context, owner, repo string, number int, channelID string, info cache.ThreadInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveThreadErr != nil {
		return m.saveThreadErr
	}
	key := fmt.Sprintf("thread:%s/%s#%d:%s", owner, repo, number, channelID)
	if m.threads == nil {
		m.threads = make(map[string]cache.ThreadInfo)
	}
	m.threads[key] = info
	return nil
}

func (m *mockStateStore) LastDM(ctx context.Context, userID, prURL string) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := userID + ":" + prURL
	if m.dmTimes != nil {
		if t, ok := m.dmTimes[key]; ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func (m *mockStateStore) RecordDM(ctx context.Context, userID, prURL string, sentAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := userID + ":" + prURL
	if m.dmTimes == nil {
		m.dmTimes = make(map[string]time.Time)
	}
	m.dmTimes[key] = sentAt
	return nil
}

func (m *mockStateStore) ListDMUsers(ctx context.Context, prURL string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dmUsers != nil {
		if users, ok := m.dmUsers[prURL]; ok {
			return users
		}
	}
	return []string{}
}

// DMMessage returns DM message info for a user and PR.
func (m *mockStateStore) DMMessage(ctx context.Context, userID, prURL string) (state.DMInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := userID + ":" + prURL
	if m.dmMessages != nil {
		if info, ok := m.dmMessages[key]; ok {
			return info, true
		}
	}
	return state.DMInfo{}, false
}

// SaveDMMessage saves DM message info for a user and PR.
func (m *mockStateStore) SaveDMMessage(ctx context.Context, userID, prURL string, info state.DMInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveDMMessageErr != nil {
		return m.saveDMMessageErr
	}
	key := userID + ":" + prURL
	if m.dmMessages == nil {
		m.dmMessages = make(map[string]state.DMInfo)
	}
	m.dmMessages[key] = info
	// Also track this user for ListDMUsers
	if m.dmUsers == nil {
		m.dmUsers = make(map[string][]string)
	}
	found := false
	for _, u := range m.dmUsers[prURL] {
		if u == userID {
			found = true
			break
		}
	}
	if !found {
		m.dmUsers[prURL] = append(m.dmUsers[prURL], userID)
	}
	return nil
}

func (m *mockStateStore) WasProcessed(ctx context.Context, eventKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.processedEvents != nil {
		return m.processedEvents[eventKey]
	}
	return false
}

func (m *mockStateStore) MarkProcessed(ctx context.Context, eventKey string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markProcessedErr != nil {
		return m.markProcessedErr
	}
	if m.processedEvents == nil {
		m.processedEvents = make(map[string]bool)
	}
	m.processedEvents[eventKey] = true
	return nil
}

func (m *mockStateStore) LastNotification(ctx context.Context, prURL string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastNotifications != nil {
		if t, ok := m.lastNotifications[prURL]; ok {
			return t
		}
	}
	return time.Time{}
}

func (m *mockStateStore) RecordNotification(ctx context.Context, prURL string, notifiedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastNotifications == nil {
		m.lastNotifications = make(map[string]time.Time)
	}
	m.lastNotifications[prURL] = notifiedAt
	return nil
}

// QueuePendingDM implements notify.Store interface for DM queue management.
func (m *mockStateStore) QueuePendingDM(ctx context.Context, dm *state.PendingDM) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingDMs = append(m.pendingDMs, dm)
	return nil
}

func (m *mockStateStore) PendingDMs(ctx context.Context, before time.Time) ([]state.PendingDM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []state.PendingDM
	for _, dm := range m.pendingDMs {
		if dm.SendAfter.Before(before) {
			result = append(result, *dm)
		}
	}
	return result, nil
}

func (m *mockStateStore) RemovePendingDM(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, dm := range m.pendingDMs {
		if dm.ID == id {
			m.pendingDMs = append(m.pendingDMs[:i], m.pendingDMs[i+1:]...)
			break
		}
	}
	return nil
}

func (*mockStateStore) Close() error {
	return nil
}

// mockSlackClient implements SlackClient for testing.
//
//nolint:govet // fieldalignment optimization would reduce test readability
type mockSlackClient struct {
	mu                    sync.Mutex
	postThreadFunc        func(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error)
	updateMessageFunc     func(ctx context.Context, channelID, timestamp, text string) error
	updateDMMessageFunc   func(ctx context.Context, userID, timestamp, text string) error
	sendDirectMessageFunc func(ctx context.Context, userID, text string) (dmChannelID, messageTS string, err error)
	isUserInChannelFunc   func(ctx context.Context, channelID, userID string) bool
	findDMMessagesFunc    func(ctx context.Context, userID, prURL string, since time.Time) ([]slackapi.DMLocation, error)
	channelHistoryFunc    func(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error)
	resolveChannelFunc    func(ctx context.Context, channelName string) string
	botInChannelFunc      func(ctx context.Context, channelID string) bool
	botInfoFunc           func(ctx context.Context) (*slack.AuthTestResponse, error)
	workspaceInfoFunc     func(ctx context.Context) (*slack.TeamInfo, error)
	publishHomeFunc       func(ctx context.Context, userID string, blocks []slack.Block) error
	apiFunc               func() *slack.Client

	// For direct workspace info control
	workspaceInfo    *slack.TeamInfo
	workspaceInfoErr bool

	// Tracking for test assertions
	postedMessages     []mockPostedMessage
	updatedMessages    []mockUpdatedMessage
	updatedDMMessage   []mockUpdatedDMMessage
	sentDirectMessages []mockSentDirectMessage
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

type mockSentDirectMessage struct {
	UserID string
	Text   string
}

func (m *mockSlackClient) PostThread(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
	m.mu.Lock()
	m.postedMessages = append(m.postedMessages, mockPostedMessage{
		ChannelID:   channelID,
		Text:        text,
		Attachments: attachments,
	})
	m.mu.Unlock()
	if m.postThreadFunc != nil {
		return m.postThreadFunc(ctx, channelID, text, attachments)
	}
	return "1234567890.123456", nil
}

func (m *mockSlackClient) UpdateMessage(ctx context.Context, channelID, timestamp, text string) error {
	m.mu.Lock()
	m.updatedMessages = append(m.updatedMessages, mockUpdatedMessage{
		ChannelID: channelID,
		Timestamp: timestamp,
		Text:      text,
	})
	m.mu.Unlock()
	if m.updateMessageFunc != nil {
		return m.updateMessageFunc(ctx, channelID, timestamp, text)
	}
	return nil
}

func (m *mockSlackClient) UpdateDMMessage(ctx context.Context, userID, prURL, text string) error {
	m.mu.Lock()
	m.updatedDMMessage = append(m.updatedDMMessage, mockUpdatedDMMessage{
		UserID: userID,
		PRURL:  prURL,
		Text:   text,
	})
	m.mu.Unlock()
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

// SendDirectMessage sends a DM to a user.
func (m *mockSlackClient) SendDirectMessage(ctx context.Context, userID, text string) (dmChannelID, messageTS string, err error) {
	m.mu.Lock()
	m.sentDirectMessages = append(m.sentDirectMessages, mockSentDirectMessage{
		UserID: userID,
		Text:   text,
	})
	m.mu.Unlock()
	if m.sendDirectMessageFunc != nil {
		return m.sendDirectMessageFunc(ctx, userID, text)
	}
	return "D" + userID, "1234567890.123456", nil
}

// IsUserInChannel checks if a user is in a channel.
func (m *mockSlackClient) IsUserInChannel(ctx context.Context, channelID, userID string) bool {
	if m.isUserInChannelFunc != nil {
		return m.isUserInChannelFunc(ctx, channelID, userID)
	}
	return false
}

// FindDMMessagesInHistory searches DM history for messages containing a PR URL.
func (m *mockSlackClient) FindDMMessagesInHistory(ctx context.Context, userID, prURL string, since time.Time) ([]slackapi.DMLocation, error) {
	if m.findDMMessagesFunc != nil {
		return m.findDMMessagesFunc(ctx, userID, prURL, since)
	}
	// Default: return empty (no DMs found in history)
	return nil, nil
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
		slackID, err := m.SlackHandle(ctx, user, owner, domain)
		if err != nil || slackID == "" {
			continue
		}
		if i > 0 && mentions != "" {
			mentions += ", "
		}
		mentions += "<@" + slackID + ">"
	}
	return mentions
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
