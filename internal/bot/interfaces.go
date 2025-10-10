// Package bot defines minimal interfaces for dependencies.
// Interfaces are defined here, where they're consumed, not where they're implemented.
// This is Go best practice: accept interfaces, return structs.
package bot

import (
	"context"

	"github.com/slack-go/slack"
)

// SlackClient defines Slack operations needed by bot.
// Small interface - only methods we actually call.
type SlackClient interface {
	PostThread(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error)
	UpdateMessage(ctx context.Context, channelID, timestamp, text string) error
	ChannelHistory(ctx context.Context, channelID string, oldest, latest string, limit int) (*slack.GetConversationHistoryResponse, error)
	ResolveChannelID(ctx context.Context, channelName string) string
	IsBotInChannel(ctx context.Context, channelID string) bool
	BotInfo(ctx context.Context) (*slack.AuthTestResponse, error)
	WorkspaceInfo(ctx context.Context) (*slack.TeamInfo, error)
	API() *slack.Client
}

// GitHubClient defines GitHub operations needed by bot.
type GitHubClient interface {
	InstallationToken(ctx context.Context) string
	Organization() string
	Client() interface{}
}

// ConfigManager defines configuration operations.
type ConfigManager interface {
	Config(org string) (interface{}, bool)
	LoadConfig(ctx context.Context, org string) error
	ReloadConfig(ctx context.Context, org string) error
	Domain(org string) string
	WorkspaceName(org string) string
	ChannelsForRepo(org, repo string) []string
	SetGitHubClient(org string, client interface{})
	SetWorkspaceName(workspaceName string)
}

// UserMapper defines user mapping operations.
type UserMapper interface {
	SlackHandle(ctx context.Context, githubUsername, organization, domain string) (string, error)
	FormatUserMentions(ctx context.Context, githubUsernames []string, organization, domain string) string
}
