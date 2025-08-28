# Ready-to-Review Slacker

[![Go Report Card](https://goreportcard.com/badge/github.com/codeGROOVE-dev/slacker)](https://goreportcard.com/report/github.com/codeGROOVE-dev/slacker)
[![GoDoc](https://godoc.org/github.com/codeGROOVE-dev/slacker?status.svg)](https://godoc.org/github.com/codeGROOVE-dev/slacker)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/codeGROOVE-dev/slacker)](go.mod)

![Ready-to-Review Slacker](media/Slackerposter.jpg)

Slack bot that tracks GitHub pull requests and notifies reviewers when it's their turn. Part of the https://codegroove.dev/ ecosystem of developer acceleration tools.

## Features

- Creates Slack threads for new PRs
- Tracks PR state with reaction emojis
- Notifies users when PRs are blocked on them
- Native Slack app home dashboard
- Configurable notification delays
- Multi-org and multi-workspace support

## Installation

```bash
git clone https://github.com/codeGROOVE-dev/slacker.git
cd slacker
make build
```

## Configuration

Set environment variables:

```bash
SLACK_BOT_TOKEN=xoxb-...
SLACK_SIGNING_SECRET=...
GITHUB_APP_ID=...
GITHUB_PRIVATE_KEY=...
GITHUB_INSTALLATION_ID=...
SPRINKLER_URL=wss://hook.g.robot-army.dev/ws  # optional
PORT=9119                                       # optional
DATA_DIR=./data                                 # optional
```

Configure repos by adding `.github/codeGROOVE/slack.yaml`:

```yaml
global:
    prefix: ":postal_horn:"
repos:
    myrepo:
        channels:
            - "#engineering"
```

## Usage

```bash
make run-server
```

Slack commands:
- `/r2r dashboard` - View your PR dashboard
- `/r2r settings` - Configure notifications
- `/r2r help` - Show help

The dashboard is also available in the app's Home tab or at https://dash.ready-to-review.dev/

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

## License

MIT
