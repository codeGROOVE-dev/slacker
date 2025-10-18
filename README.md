# Ready-to-Review Slacker

[![Go Report Card](https://goreportcard.com/badge/github.com/codeGROOVE-dev/slacker)](https://goreportcard.com/report/github.com/codeGROOVE-dev/slacker)
[![GoDoc](https://godoc.org/github.com/codeGROOVE-dev/slacker?status.svg)](https://godoc.org/github.com/codeGROOVE-dev/slacker)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Go Version](https://img.shields.io/github/go-mod/go-version/codeGROOVE-dev/slacker)](go.mod)

![Ready-to-Review Slacker](media/Slackerposter.jpg)

Slack bot that tracks GitHub pull requests and notifies reviewers when it's their turn. Part of the https://codegroove.dev/ ecosystem of developer acceleration tools.

## Quick Start

Ready to get started? Install the Slack app and configure your repositories:

<a href="https://slack.com/oauth/v2/authorize?client_id=9426269265270.9443955134789&scope=channels:history,channels:read,chat:write,chat:write.public,commands,im:write,reactions:write,team:read,users:read,users:read.email,groups:read,groups:history&user_scope="><img alt="Add to Slack" height="40" width="139" src="https://platform.slack-edge.com/img/add_to_slack.png" srcSet="https://platform.slack-edge.com/img/add_to_slack.png 1x, https://platform.slack-edge.com/img/add_to_slack@2x.png 2x" /></a>

**→ [Setup Guide](docs/SETUP.md)** - Complete setup instructions, emoji meanings, and notification settings

## Features

- Creates Slack threads for new PRs with status emojis
- Smart notifications: Delays DMs if user already notified in channel
- Native Slack app home dashboard
- Channel auto-discovery: repos automatically map to same-named channels
- Configurable notification settings via YAML in your repos
- Multi-org and multi-workspace support
- Daily reminder system with timezone detection

## Self-Hosting

Want to run your own instance? Follow the installation steps below.

```bash
git clone https://github.com/codeGROOVE-dev/slacker.git
cd slacker
make build
```

See [docs/SLACK_SETUP.md](docs/SLACK_SETUP.md) for detailed Slack app configuration.

## Slack App Configuration

### Required Bot Token Scopes
Your Slack app needs these OAuth scopes:

**Bot Token Scopes:**
- `app_mentions:read` - Read mentions of the app
- `channels:history` - View messages in public channels
- `channels:read` - View basic information about public channels
- `chat:write` - Send messages as the bot
- `chat:write.public` - Send messages to channels the bot isn't a member of
- `commands` - Add shortcuts and/or slash commands
- `im:history` - View messages in direct messages
- `im:read` - View basic information about direct messages
- `im:write` - Start direct messages with people
- `reactions:write` - Add and edit emoji reactions
- `team:read` - View workspace name and domain (required for workspace validation)
- `users:read` - View people in the workspace

### Event Subscriptions
Enable these events:
- `app_home_opened` - User opened the app home
- `app_mention` - App was mentioned
- `message.channels` - Message posted to channel
- `message.im` - Message posted to direct message

## Environment Configuration

Set environment variables:

```bash
SLACK_BOT_TOKEN=xoxb-...
SLACK_SIGNING_SECRET=...
GITHUB_APP_ID=...
GITHUB_PRIVATE_KEY=...
GITHUB_INSTALLATION_ID=...
```

Configure repos by adding `.codeGROOVE/slack.yaml`:

```yaml
global:
    prefix: ":postal_horn:"
    slack: myorg-workspace.slack.com
    channel_notify_delay_mins: 60  # Default: 60 minutes
    daily_reminders: true          # Default: true

channels:
    all-repos:
        repos:
            - "*"
    
    engineering:
        repos:
            - myrepo
            - anotherepo
    
    # Mute auto-discovered channel for goose repo
    goose:
        mute: true
    
    # Override auto-discovery - send myrepo to announcements instead of #myrepo
    announcements:
        repos:
            - myrepo
```

## Channel Auto-Discovery

**By default, repos automatically map to channels with the same name:**
- Repository `goose` → Channel `#goose`  
- Repository `my-service` → Channel `#my-service`
- Repository `api-server` → Channel `#api-server`

**Override auto-discovery by:**
1. **Explicit mapping**: Add repo to a channel's `repos` list
2. **Muting**: Set `mute: true` for the auto-discovered channel name
3. **Wildcard**: Use `"*"` in a channel to catch all repos

## Usage

Slack commands:
- `/r2r dashboard` - View your PR dashboard
- `/r2r help` - Show help

The dashboard is also available in the app's Home tab or at https://dash.ready-to-review.dev/

## Smart Notification Logic

- **Channel notifications**: If a user is tagged in a public channel they're in, DMs are delayed by `channel_notify_delay_mins` (default: 60 minutes)
- **Immediate DMs**: If a user isn't in the notification channel, they get immediate DMs
- **Daily reminders**: Sent between 8-9am local time if enabled and >8 hours since last notification
- **Anti-spam**: Multiple DMs to the same user are rate limited

## Development

```bash
make fmt        # Format code
make lint       # Run linters
make test       # Run tests
make build      # Build binary
```

## Dependencies

- [sprinkler](https://github.com/codeGROOVE-dev/sprinkler) - WebSocket hub for GitHub webhooks
- [turnclient](https://github.com/codeGROOVE-dev/turnclient) - PR state analysis
