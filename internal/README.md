# Internal Packages

This directory contains implementation packages for Slacker.
These packages are **internal-only** and cannot be imported by external projects.

## Architecture

Slacker follows clean architecture principles with clear package boundaries:

```
internal/
├── bot/          # Core orchestration - coordinates all operations
├── config/       # Configuration from .codeGROOVE/slack.yaml
├── github/       # GitHub API client and webhook handling
├── notify/       # Notification scheduling and delivery
├── slack/        # Slack API client and event handlers
└── usermapping/  # GitHub ↔ Slack user mapping
```

## Design Principles

### 1. Accept Interfaces, Return Structs

Interfaces are defined where they're consumed (e.g., `bot/interfaces.go`),
not where they're implemented. This makes testing easier and dependencies clearer.

### 2. Minimal Interfaces

Each interface contains only the methods actually used by its consumer.
No "kitchen sink" interfaces.

### 3. No Circular Dependencies

Dependency graph flows one direction:
```
cmd/server → bot → {slack, github, notify, config, usermapping}
           notify → slack
      usermapping → slack
```

### 4. Context All The Way Down

Every operation that can block accepts `context.Context` as first parameter.
Contexts are passed from `main()`, never created in libraries.

### 5. Structured Logging

All packages use `log/slog` for structured logging.
Log levels: Debug, Info, Warn, Error.

## Package Descriptions

### bot

Coordinates between GitHub events, Slack notifications, and configuration.
Owns the main event processing loop and PR state machine.

**Key types:**
- `Coordinator` - Main orchestrator
- `ThreadCache` - In-memory cache of PR→thread mappings

### config

Reads YAML configuration from GitHub repos (`.codeGROOVE/slack.yaml`).
Handles workspace validation and channel auto-discovery.

**Key types:**
- `Manager` - Manages configs for multiple orgs
- `OrgConfig` - Per-organization configuration

### github

GitHub App authentication and API client management.
Handles installation tokens and multi-org support.

**Key types:**
- `Manager` - Manages clients for multiple GitHub installations
- `Client` - Per-organization GitHub API client

### notify

Notification scheduling with smart DM delays and anti-spam logic.
Tracks when users were tagged to implement delayed reminders.

**Key types:**
- `Manager` - Notification orchestrator
- `NotificationTracker` - Tracks DM timing per user/PR

### slack

Slack API client with retry logic, caching, and rate limiting.
Handles events, interactions, and multi-workspace support.

**Key types:**
- `Manager` - Manages clients for multiple Slack workspaces
- `Client` - Per-workspace Slack API client with caching
- `EventRouter` - Routes events to correct workspace

### usermapping

Maps GitHub usernames to Slack user IDs using email matching.
Uses caching to minimize API calls.

**Key types:**
- `Service` - Email-based user mapping with cache
- `UserMapping` - Cached mapping with confidence score

## Testing

When you need to test code that depends on these packages:

1. Define an interface in your test package
2. Create a simple mock that implements it
3. Pass the mock to your code

Example:
```go
// bot_test.go
type mockSlackClient struct {
    postThreadFunc func(context.Context, string, string, []slack.Attachment) (string, error)
}

func (m *mockSlackClient) PostThread(ctx context.Context, channelID, text string, attachments []slack.Attachment) (string, error) {
    if m.postThreadFunc != nil {
        return m.postThreadFunc(ctx, channelID, text, attachments)
    }
    return "ts_123", nil
}
```

Keep mocks in test files. Don't create a separate `mocks/` directory unless you're sharing mocks across many packages.

## Adding New Packages

Before adding a new package to `internal/`, ask:

1. Is this functionality genuinely internal-only?
2. Does it have a clear single responsibility?
3. Does it avoid circular dependencies?
4. Can it be tested independently?

If yes to all, create the new package with:
- Clear package-level documentation
- Exported types with clear names
- Minimal public surface area
