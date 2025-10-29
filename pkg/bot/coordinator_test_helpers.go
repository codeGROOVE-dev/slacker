package bot

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	postedMessages  []mockPostedMessage
	updatedMessages []mockUpdatedMessage
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

func (m *mockSlackClient) UpdateDMMessage(ctx context.Context, userID, timestamp, text string) error {
	if m.updateDMMessageFunc != nil {
		return m.updateDMMessageFunc(ctx, userID, timestamp, text)
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
