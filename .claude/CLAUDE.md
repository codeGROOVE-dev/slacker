# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

reviewGOOSE:Slack is the Slack integration for reviewGOOSE — an elegant modern Slack bot written in Go that integrates with GitHub to streamline PR review workflows. The bot provides real-time notifications, dashboard views, and multi-org/multi-Slack support.

## Core Features

### 1. Channel Thread Management
- Start threads in Slack channels when new PRs are created
- Update message prefix emoji based on PR state:
  - `:new:` - newly published (appears for ~15 seconds before state changes)
  - `:test_tube:` - tests running/pending
  - `:cockroach:` - tests broken (blocked on author)
  - `:hourglass:` - waiting on review
  - `:carpentry_saw:` - approved but needs work (blocked on author)
  - `:white_check_mark:` - reviewed & approved (blocked on author)
  - `:rocket:` - merged
  - `:x:` - closed but not merged
- Format: `<prefix_emoji> Update README.md • goose#51 by @slackUser` (with link to PR, link previews disabled)
- PR URLs include state suffix for debugging (e.g., `?st=tests_broken`, `?st=awaiting_review`)
- Post follow-up comments when reviewers are assigned

### 2. User Dashboard
- Native Slack app home tab with Block Kit UI showing incoming/outgoing PRs
- Highlights PRs blocked on the user
- Clean, settings-free interface focusing on PR status
- Alternative web dashboard available at https://reviewgoose.dev/

### 3. Smart Notifications
- **Smart DM Logic**: If user tagged in channel, delay DMs by configured time (default: 65min)
  - If user is IN the channel where they were tagged → wait for configured delay before sending DM
  - If user is NOT in the channel where they were tagged → send DM immediately
  - Set `reminder_dm_delay: 0` to disable delayed reminders
  - **One DM per user per PR**: Even if PR is posted to multiple channels, each user gets exactly one DM
- **Daily reminders**: Send between 8-9am local time if enabled and >8 hours since last notification
- **Anti-spam**: Rate limit DMs to same user (1min minimum between DMs)
- Format: `:hourglass: Update README.md <url|goose#51> · author → review` (matches channel message style)

### 4. Configuration
- Read YAML config from `/.codeGROOVE/slack.yaml` in target repos
- Only posts to Slack if workspace name matches
- No user settings UI - all controlled via YAML config
- **Auto-discovery**: Repos automatically map to channels with same name unless overridden
- Config format:
```yaml
global:
    prefix: ":postal_horn:"
    slack: codegroove-workspace.slack.com
    reminder_dm_delay: 65  # Minutes to wait before DMing users tagged in channel (0 = disabled)
    daily_reminders: true

channels:
    # Mute auto-discovered #goose channel
    goose:
        mute: true

    # Catch-all for repos without specific channels
    all-codegroove:
        repos:
            - "*"

    # Override auto-discovery - explicit mapping with custom delay
    social:
        repos:
            - sprinkler
            - slacker
        reminder_dm_delay: 30  # Override global delay for this channel
```

### 5. Channel Auto-Discovery
- **Default behavior**: `codeGROOVE-dev/goose` → `#goose` channel
- **Explicit override**: Add repo to channel's `repos` list
- **Muting**: Set `mute: true` for auto-discovered channel name
- **Wildcard fallback**: Use `"*"` to catch unmapped repos

## Development Commands

```bash
make build        # Build the server binary
make test         # Run tests with race detection
make lint         # Run comprehensive linting (golangci-lint, yamllint, shellcheck)
make fmt          # Format code with go fmt and gofmt -s
make vet          # Run go vet
make run-server   # Start the bot server
make clean        # Clean build artifacts
```

## Architecture

### External Dependencies
- `github.com/codeGROOVE-dev/sprinkler` - WebSocket hub for GitHub webhook events
- `github.com/codeGROOVE-dev/turnclient` - PR state analysis and blocking detection
- `github.com/slack-go/slack` - Official Slack API client
- `github.com/google/go-github/v50` - GitHub API client

### Project Structure
```
slacker/
├── cmd/server/main.go      # Main server entry point
├── pkg/
│   ├── bot/               # Core bot logic and coordination
│   ├── config/            # YAML configuration management
│   ├── github/            # GitHub integration and webhook handling
│   ├── notify/            # Notification scheduling and delivery
│   ├── slack/             # Slack API integration and Block Kit UI
│   └── state/             # PR and user state management
├── Makefile               # Build and development commands
├── go.mod                 # Go module dependencies
└── .golangci.yml          # Comprehensive linting configuration
```

## Key Implementation Guidelines

### GitHub App Authentication
- Authenticate as GitHub App (not OAuth) for multi-org support
- Handle installation events and permission changes gracefully
- Store installation tokens with appropriate refresh logic

### Slack Integration
- Use Events API for real-time updates
- Handle app_home_opened events to update dashboard
- Build dashboard using Block Kit components
- Cache user timezone and presence information
- Disable link previews for GitHub URLs in messages

### Required Slack Bot Scopes
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
- `team:read` - **View workspace name and domain (required for workspace validation)**
- `users:read` - View people in the workspace
- `users:read.email` - **Access user email addresses (required for GitHub→Slack user mapping)**

### State Management
- Track PR states and transitions for notification logic
- Cache user blocking status to minimize API calls
- Store user preferences and notification history
- Handle config file updates via webhook on merge to .github repo

### Notification Logic
- Check Slack presence before sending delayed notifications
- Respect user timezone for daily reminders (use ../gutz for detection if needed)
- Track notification history to prevent duplicates
- Queue notifications for reliability

### Error Handling
- Graceful degradation when APIs are unavailable
- Comprehensive structured logging for debugging
- Retry logic with exponential backoff
- Health check endpoints for monitoring

## Testing Requirements

- All new functionality must have unit tests
- Integration tests for Slack and GitHub interactions
- Mock external dependencies appropriately
- Tests must pass with race detection enabled
- Aim for >80% code coverage

## Security Considerations

- Never log or expose tokens/secrets
- Validate all webhook signatures
- Sanitize user input in messages
- Use context timeouts for all external calls
- Implement rate limiting for API endpoints

## Style Notes

The bot should feel like "a collaboration between Craigslist & Steve Jobs, with input from James Brown" - clean and functional with subtle personality touches in messaging and interactions.