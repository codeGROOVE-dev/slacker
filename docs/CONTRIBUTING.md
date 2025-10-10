# Contributing to Slacker

This codebase follows modern Go best practices with Rob Pike's philosophy: **simple, clear, maintainable**.

## Code Style

### The Rob Pike Way

1. **Clear is better than clever**
   ```go
   // Good - obvious
   if user == "" {
       return ErrNoUser
   }

   // Avoid - too clever
   return map[bool]error{user == "": ErrNoUser}[true]
   ```

2. **A little copying is better than a little dependency**
   - Duplicate small functions rather than create complex abstractions
   - Keep packages independent

3. **Make zero values useful**
   ```go
   var cache Cache  // Immediately usable, no New() required
   ```

4. **Interfaces are small**
   - 1-3 methods is ideal
   - Defined where consumed, not where implemented
   - See `internal/bot/interfaces.go`

### Go Conventions

1. **No `Get` prefixes** - `User()` not `GetUser()`
2. **Contexts first** - `func Do(ctx context.Context, ...)`
3. **Accept interfaces, return structs**
4. **Error wrapping** - `fmt.Errorf("action failed: %w", err)`
5. **Short names in small scopes** - `i`, `n`, `ctx` are fine

## Package Organization

### `internal/` Directory

All implementation lives in `internal/` - external projects **cannot import** these packages.

```
internal/
├── bot/          # Orchestration
├── config/       # Configuration
├── github/       # GitHub client
├── notify/       # Notifications
├── slack/        # Slack client
└── usermapping/  # User mapping
```

Each package has:
- Single responsibility
- No circular dependencies
- Clear public API
- Package-level documentation

### Adding a Package

Before creating a new package, ask:

1. Does it have a single, clear purpose?
2. Can it be tested independently?
3. Is it genuinely internal-only?
4. Does it avoid circular dependencies?

If yes, create it in `internal/`.

## Testing

### Writing Tests

Use table-driven tests:

```go
func TestFoo(t *testing.T) {
    tests := []struct {
        name string
        in   string
        want string
    }{
        {"empty", "", ""},
        {"simple", "hello", "HELLO"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := Foo(tt.in)
            if got != tt.want {
                t.Errorf("got %q, want %q", got, tt.want)
            }
        })
    }
}
```

### Mocking

Define interfaces in test files, not in `mocks/` packages:

```go
// foo_test.go
type slackClient interface {
    PostMessage(ctx context.Context, channel, text string) error
}

type mockSlack struct {
    postFunc func(context.Context, string, string) error
}

func (m *mockSlack) PostMessage(ctx context.Context, channel, text string) error {
    if m.postFunc != nil {
        return m.postFunc(ctx, channel, text)
    }
    return nil
}
```

## Error Handling

### Pattern

```go
if err != nil {
    return fmt.Errorf("descriptive context: %w", err)
}
```

### Checking Errors

```go
if errors.Is(err, ErrNotFound) {
    // Handle specifically
}
```

### Logging Errors

```go
slog.Error("operation failed",
    "error", err,
    "context_field", value)
```

## Logging

Use structured logging with `slog`:

```go
slog.Info("processing PR",
    "owner", owner,
    "repo", repo,
    "pr", prNumber,
    "state", prState)
```

**Levels:**
- `Debug` - Verbose details (development only)
- `Info` - Normal operations
- `Warn` - Recoverable issues
- `Error` - Requires attention

**Don't log:**
- Secrets or tokens
- Personally identifiable information
- Expected errors (use Debug)

## Concurrency

### Safe Patterns

1. **Protect shared state**
   ```go
   type Cache struct {
       mu    sync.RWMutex
       data  map[string]string
   }

   func (c *Cache) Get(key string) string {
       c.mu.RLock()
       defer c.mu.RUnlock()
       return c.data[key]
   }
   ```

2. **Use WaitGroups**
   ```go
   var wg sync.WaitGroup
   for _, item := range items {
       wg.Add(1)
       go func(i Item) {
           defer wg.Done()
           process(i)
       }(item)
   }
   wg.Wait()
   ```

3. **Propagate context**
   ```go
   func process(ctx context.Context) error {
       select {
       case <-ctx.Done():
           return ctx.Err()
       case <-done:
           return nil
       }
   }
   ```

### Avoid

- Creating goroutines without cleanup
- Goroutines that can leak
- Shared state without locking
- Creating contexts in libraries

## Development Workflow

### Before Committing

```bash
make fmt        # Format code
make lint       # Run linters
make test       # Run tests
make build      # Verify build
```

All must pass.

### Commit Messages

```
Short summary (50 chars or less)

More detailed explanation if needed. Wrap at 72 characters.

- Bullet points are fine
- Use present tense: "add feature" not "added feature"
```

## Common Patterns

### Retry with Backoff

```go
err := retry.Do(
    func() error {
        return doThing()
    },
    retry.Attempts(5),
    retry.Delay(2*time.Second),
    retry.MaxDelay(2*time.Minute),
    retry.DelayType(retry.BackOffDelay),
    retry.MaxJitter(time.Second),
    retry.Context(ctx),
)
```

### Graceful Shutdown

```go
eg, ctx := errgroup.WithContext(ctx)

eg.Go(func() error {
    return server.ListenAndServe()
})

eg.Go(func() error {
    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(
        context.WithoutCancel(ctx),
        5*time.Second,
    )
    defer cancel()
    return server.Shutdown(shutdownCtx)
})

return eg.Wait()
```

### Cache with TTL

```go
type cache struct {
    mu      sync.RWMutex
    entries map[string]entry
}

type entry struct {
    value     interface{}
    expiresAt time.Time
}

func (c *cache) Get(key string) (interface{}, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()

    e, ok := c.entries[key]
    if !ok || time.Now().After(e.expiresAt) {
        return nil, false
    }
    return e.value, true
}
```

## Performance

### Optimization Rules

1. **Make it work** - Correctness first
2. **Make it right** - Clean code
3. **Make it fast** - Only if needed

### When to Optimize

- **Profile first** - Don't guess
- **Big O matters** - Algorithm beats micro-optimization
- **Cache wisely** - Memory is cheaper than latency
- **Concurrency helps** - But adds complexity

### Caching Strategy

Current caches and their TTLs:

- Slack team info: 1 hour
- Slack bot info: 1 hour
- Channel resolution: 1 hour
- User mappings: 24 hours
- PR threads: indefinite (process lifetime)

## Security

### Rules

1. **Never log secrets** - Check before adding logging
2. **Validate input** - All user-provided data
3. **Verify signatures** - All webhook requests
4. **Use timeouts** - All external calls
5. **Sanitize errors** - Don't leak internal details

### Secrets Management

Secrets come from environment or Google Secret Manager:

```go
token := os.Getenv("SLACK_TOKEN")
if token == "" {
    token, _ = gsm.Fetch(ctx, "slack-token")
}
```

Never commit secrets. Never log secrets.

## Questions?

Read:
1. This file
2. `ARCHITECTURE.md` - System design
3. `internal/README.md` - Package structure
4. `.claude/CLAUDE.md` - Project overview

Then ask! File an issue or start a discussion.
