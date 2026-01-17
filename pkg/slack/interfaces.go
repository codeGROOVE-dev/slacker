package slack

import (
	"context"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/usermapping"
)

// GitHubManager defines the interface for GitHub client management.
// This interface allows for easier testing by enabling mock implementations.
type GitHubManager interface {
	AllOrgs() []string
	ClientForOrg(org string) (*github.Client, bool)
}

// ConfigManager defines the interface for configuration management.
// This interface allows for easier testing by enabling mock implementations.
type ConfigManager interface {
	Config(org string) (*config.RepoConfig, bool)
}

// UserMapper defines the interface for Slack-to-GitHub user mapping.
// This interface allows for easier testing by enabling mock implementations.
type UserMapper interface {
	LookupGitHub(ctx context.Context, slackAPI usermapping.SlackAPI, slackUserID, org, emailDomain string) (*usermapping.ReverseMapping, error)
	SetOverrides(overrides map[string]string)
}
