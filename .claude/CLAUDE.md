# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Ready to Review is an elegant modern Slack bot written in Go that integrates with GitHub to streamline PR review workflows. The bot provides real-time notifications, dashboard views, and multi-org/multi-Slack support.

## Core Features

### 1. Channel Thread Management
- Start threads in Slack channels when new PRs are created
- Add reaction emojis based on PR state:
  - `:test_tube:` - tests running/pending
  - `:broken_heart:` - tests broken (blocked on author)
  - `:hourglass:` - waiting on review
  - `:carpentry_saw:` - approved but needs work (blocked on author)
  - `:check:` - reviewed & approved (blocked on author)
  - `:pray:` - merged
  - `:face_palm:` - closed but not merged
- Format: `:postal_horn: Update README.md • goose#51 by @slackUser` (with link to PR, link previews disabled)
- Post follow-up comments when reviewers are assigned with checkmark reactions when they review

### 2. User Dashboard
- Native Slack app home tab with Block Kit UI showing incoming/outgoing PRs
- Highlights PRs blocked on the user
- User settings in app home:
  - Enable real-time notifications [default: on]
  - Notification delay after channel post [15min, 30min, 60min, 2hr] [default: 30min]
  - Enable daily reminders [default: on]
- Alternative web dashboard available at https://dash.ready-to-review.dev/

### 3. Smart Notifications
- Real-time: Send DM when user is blocking a PR (respects channel notification delay and Slack active status)
- Daily reminders: Send between 8-9am local time if >8 hours since last notification
- Format: `:postal_horn: Update README.md • goose#51 by @slackUser - waiting for your review`

### 4. Configuration
- Read YAML config from `/.github/codeGROOVE/slack.yaml` in target repos
- Config format:
```yaml
global:
    prefix: ":postal_horn:"
repos:
    goose:
        channels:
            - #goose
    .github:
        channels:
            - #goose
            - #eng
```

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
- `github.com/ready-to-review/turnclient` - PR state analysis and blocking detection
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
- Handle interactive components for settings
- Cache user timezone and presence information
- Disable link previews for GitHub URLs in messages

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