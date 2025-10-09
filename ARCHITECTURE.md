# Architecture

Slacker is a modern Go application following clean architecture principles.

## Principles

This codebase follows Rob Pike's philosophy:

> "Simplicity is the ultimate sophistication."
> "A little copying is better than a little dependency."
> "The bigger the interface, the weaker the abstraction."

### Key Design Decisions

1. **`internal/` packages** - Implementation is not importable externally
2. **Interfaces at consumption** - Defined where used, not where implemented
3. **No circular dependencies** - Clean dependency graph
4. **Context everywhere** - All blocking operations accept `context.Context`
5. **Minimal public surface** - Only export what's necessary

## Directory Structure

```
slacker/
├── cmd/
│   ├── server/              # Main server binary
│   └── slack-registrar/     # Slack app registration tool
├── internal/                # Implementation packages (not importable)
│   ├── bot/                # Core orchestration
│   ├── config/             # YAML configuration
│   ├── github/             # GitHub API client
│   ├── notify/             # Notification logic
│   ├── slack/              # Slack API client
│   └── usermapping/        # GitHub↔Slack mapping
└── .claude/                # Claude Code configuration

```

## Data Flow

```
GitHub Webhook
    ↓
Sprinkler (WebSocket)
    ↓
bot.Coordinator.processEvent()
    ├→ Load config from .codeGROOVE/slack.yaml
    ├→ Analyze PR with turnclient
    ├→ Post to Slack channels
    └→ Schedule DM notifications
        ↓
    notify.Manager
        ├→ Check if user active
        ├→ Apply delay logic
        └→ Send DM via slack.Client
```

## State Management

State is kept **minimal** and **in-memory**:

- **PR threads** - Cached in `bot.ThreadCache` (map of PR → Slack thread)
- **Notifications** - Tracked in `notify.NotificationTracker` (when we last DM'd)
- **User mappings** - Cached in `usermapping.Service` (GitHub → Slack, 24h TTL)
- **Config** - Cached in `config.Manager` (per-org YAML, reloaded on push)

No database. State resets on restart, which is acceptable.

## Concurrency

### Safe Patterns

- All caches use `sync.RWMutex` for thread-safety
- Channel processing uses `sync.WaitGroup` for parallel execution
- DM sending runs in separate goroutines with timeouts
- Contexts propagate cancellation through the stack

### Key Goroutines

1. **HTTP server** - Handles Slack webhooks
2. **Bot coordinators** - One per GitHub org (long-running)
3. **Notification scheduler** - Checks for pending notifications
4. **DM senders** - Fire-and-forget with 2min timeout

## Error Handling

Errors are **wrapped** for context:

```go
if err != nil {
    return fmt.Errorf("failed to post thread: %w", err)
}
```

Then checked with `errors.Is()` for specific handling.

### Retry Strategy

External API calls use exponential backoff with jitter:

```go
retry.Do(fn,
    retry.Attempts(5),
    retry.Delay(2*time.Second),
    retry.MaxDelay(2*time.Minute),
    retry.DelayType(retry.BackOffDelay),
    retry.MaxJitter(time.Second),
)
```

## Testing Strategy

### Current State

- Unit tests for `usermapping` package
- Integration tests would require mocking external APIs

### How to Add Tests

1. Define interface in your test file:
   ```go
   type slackClient interface {
       PostThread(ctx, channelID, text string) (string, error)
   }
   ```

2. Create simple mock:
   ```go
   type mockSlack struct {
       postThreadFunc func(context.Context, string, string) (string, error)
   }
   ```

3. Use table-driven tests:
   ```go
   tests := []struct{
       name string
       want string
   }{
       {"case1", "expected1"},
   }
   ```

**Don't create a separate mocks package unless you need to share mocks.**

## Configuration

Configuration is **pull-based** from GitHub repos:

```yaml
# .codeGROOVE/slack.yaml in target repo
global:
    slack: workspace.slack.com
    reminder_dm_delay: 65  # minutes

channels:
    engineering:
        repos: ["backend", "frontend"]
```

The bot reads this file when processing PRs. Changes take effect on next PR event.

## Deployment

Built as a single static binary. No runtime dependencies.

### Environment Variables

```bash
GITHUB_APP_ID=123456
GITHUB_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----
SLACK_SIGNING_SECRET=abc123
SPRINKLER_URL=wss://sprinkler.example.com/ws
```

Secrets are fetched from Google Secret Manager if not in environment.

### Health Checks

- `/health` - Basic liveness (is server responding?)
- `/healthz` - Detailed readiness (are coordinators running?)

## Performance

### Caching Strategy

- **Slack API responses** - Cached with TTL (team info: 1h, bot info: 1h)
- **Channel resolution** - Cached to avoid repeated lookups
- **User mappings** - 24h TTL, lazy cleanup
- **PR threads** - Indefinite (until coordinator restarts)

### Optimizations

1. **Parallel channel processing** - WaitGroup for concurrent Slack posts
2. **Async DM sending** - Don't block PR processing
3. **Lazy caching** - Only cache on first miss
4. **Context timeouts** - 30s for turnclient, 2min for DMs

## Security

1. **Webhook signature verification** - All Slack requests verified with HMAC
2. **Token isolation** - Each workspace has separate Slack token in GSM
3. **No token logging** - Secrets never logged
4. **Rate limiting** - Built into retry logic
5. **Input validation** - Channel names, user IDs sanitized

## Observability

### Logging

Structured logging with `slog`:

```go
slog.Info("processing PR",
    "owner", owner,
    "repo", repo,
    "number", prNumber,
    "state", prState)
```

Log levels: Debug (development), Info (production), Warn (recoverable), Error (requires attention).

### Future: Metrics

Add Prometheus metrics:
```go
prProcessed.WithLabelValues(owner, repo, state).Inc()
apiLatency.WithLabelValues("slack", "post_message", "200").Observe(duration)
```

## Common Patterns

### Context Usage

```go
// Pass context through
func process(ctx context.Context, ...) error {
    // Use for cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    case result := <-ch:
        // ...
    }
}
```

### Error Wrapping

```go
if err != nil {
    return fmt.Errorf("operation failed for %s: %w", id, err)
}
```

### Graceful Shutdown

```go
eg, ctx := errgroup.WithContext(ctx)
eg.Go(func() error {
    <-ctx.Done()
    return server.Shutdown(context.WithTimeout(context.Background(), 5*time.Second))
})
```

## Future Enhancements

Potential improvements (not currently needed):

1. **Persistent cache** - Redis for state across restarts
2. **Circuit breakers** - Prevent cascade failures
3. **Distributed tracing** - OpenTelemetry
4. **Metrics** - Prometheus/Grafana
5. **Integration tests** - Test harness with mocks

The current design supports all of these without major refactoring.
