package notify

import (
	"context"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/config"
	"github.com/codeGROOVE-dev/slacker/internal/slack"
	slackapi "github.com/slack-go/slack"
)

// SlackManager interface abstracts slack.Manager for testability.
// This allows us to mock Slack interactions in unit tests.
type SlackManager interface {
	Client(ctx context.Context, teamID string) (SlackClient, error)
}

// SlackClient interface abstracts slack.Client methods used by notify package.
// This enables unit testing of notification logic without real Slack API calls.
type SlackClient interface {
	// User operations
	IsUserActive(ctx context.Context, userID string) bool
	IsUserInChannel(ctx context.Context, channelID, userID string) bool
	UserTimezone(ctx context.Context, userID string) (string, error)

	// DM operations
	SendDirectMessage(ctx context.Context, userID, text string) (dmChannelID, messageTS string, err error)
	HasRecentDMAboutPR(ctx context.Context, userID, prURL string) (bool, error)
	SaveDMMessageInfo(ctx context.Context, userID, prURL, dmChannelID, messageTS, message string) error

	// Legacy - for usermapping compatibility
	API() *slackapi.Client
}

// ConfigManager interface abstracts config.Manager for testability.
// Used by Manager for notification preferences.
type ConfigManager interface {
	DailyRemindersEnabled(org string) bool
	ReminderDMDelay(org, channel string) int
}

// ConfigProvider provides configuration for daily digests.
// Used by DailyDigestScheduler.
type ConfigProvider interface {
	DailyRemindersEnabled(org string) bool
	Domain(org string) string
	Config(org string) (*config.RepoConfig, bool)
}

// StateProvider provides state storage for daily digests.
// Used by DailyDigestScheduler.
type StateProvider interface {
	LastDigest(userID, date string) (time.Time, bool)
	RecordDigest(userID, date string, sentAt time.Time) error
	LastDM(userID, prURL string) (time.Time, bool)
}

// slackManagerAdapter adapts concrete slack.Manager to implement SlackManager interface.
type slackManagerAdapter struct {
	manager *slack.Manager
}

func (a *slackManagerAdapter) Client(ctx context.Context, teamID string) (SlackClient, error) {
	// slack.Manager.Client returns *slack.Client
	// *slack.Client implements SlackClient through structural typing
	return a.manager.Client(ctx, teamID)
}

// WrapSlackManager wraps a concrete slack.Manager to implement SlackManager interface.
// This enables dependency injection and testing.
func WrapSlackManager(manager *slack.Manager) SlackManager {
	return &slackManagerAdapter{manager: manager}
}
