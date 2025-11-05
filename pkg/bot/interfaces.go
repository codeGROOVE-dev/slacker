package bot

// Interfaces are defined here, where they're consumed, not where they're implemented.
// This is Go best practice: accept interfaces, return structs.

import (
	"context"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	slackapi "github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/slack-go/slack"
)

// SlackClient defines Slack operations needed by bot.
// Small interface - only methods we actually call.
//
//nolint:interfacebloat // 13 methods needed for Slack integration
type SlackClient interface {
	PostThread(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error)
	UpdateMessage(ctx context.Context, channelID, timestamp, text string) error
	UpdateDMMessage(ctx context.Context, userID, prURL, text string) error
	SendDirectMessage(ctx context.Context, userID, text string) (dmChannelID, messageTS string, err error)
	IsUserInChannel(ctx context.Context, channelID, userID string) bool
	FindDMMessagesInHistory(ctx context.Context, userID, prURL string, since time.Time) ([]slackapi.DMLocation, error)
	ChannelHistory(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error)
	ResolveChannelID(ctx context.Context, channelName string) string
	IsBotInChannel(ctx context.Context, channelID string) bool
	BotInfo(ctx context.Context) (*slack.AuthTestResponse, error)
	WorkspaceInfo(ctx context.Context) (*slack.TeamInfo, error)
	PublishHomeView(ctx context.Context, userID string, blocks []slack.Block) error
	API() *slack.Client
}

// GitHubClient defines GitHub operations needed by bot.
type GitHubClient interface {
	InstallationToken(ctx context.Context) string
	Organization() string
	Client() any
	FindPRsForCommit(ctx context.Context, owner, repo, commitSHA string) ([]int, error)
	RefreshToken(ctx context.Context) error
}

// ConfigManager defines configuration operations.
type ConfigManager interface {
	Config(org string) (*config.RepoConfig, bool)
	LoadConfig(ctx context.Context, org string) error
	ReloadConfig(ctx context.Context, org string) error
	Domain(org string) string
	WorkspaceName(org string) string
	ChannelsForRepo(org, repo string) []string
	ReminderDMDelay(org, channelName string) int
	SetGitHubClient(org string, client any)
	SetWorkspaceName(workspaceName string)
}

// UserMapper defines user mapping operations.
type UserMapper interface {
	SlackHandle(ctx context.Context, githubUsername, organization, domain string) (string, error)
	FormatUserMentions(ctx context.Context, githubUsernames []string, organization, domain string) string
}

// PRSearcher defines GitHub PR search operations for polling.
type PRSearcher interface {
	ListOpenPRs(ctx context.Context, org string, updatedSinceHours int) ([]github.PRSnapshot, error)
	ListClosedPRs(ctx context.Context, org string, updatedSinceHours int) ([]github.PRSnapshot, error)
}
