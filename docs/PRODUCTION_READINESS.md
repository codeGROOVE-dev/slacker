# Production Readiness Assessment

## ✅ What's Already Solid

### 1. Error Handling & Retry Logic ⭐
**Excellent implementation**
- Comprehensive exponential backoff with jitter
- Rate limit detection and handling
- Unrecoverable errors properly marked (no retry loops)
- All Slack API calls have retry logic
- Sprinkler client has infinite retry loop with token refresh

**Examples:**
- bot_sprinkler.go:179-342 - Infinite retry loop with auth refresh
- slack.go:191-218 - PostThread with 5 retries + backoff
- All external API calls use `github.com/codeGROOVE-dev/retry`

### 2. Concurrency Safety ⭐
**Well implemented**
- ThreadCache uses sync.RWMutex properly (bot.go:33-58)
- NotificationTracker uses sync.RWMutex (notify/tracker.go:12-99)
- apiCache uses sync.RWMutex (slack.go:38-80)
- WaitGroup for parallel channel processing (bot.go:713-724)
- No data races detected in patterns

### 3. Timeout & Context Handling
**Good patterns:**
- Turnclient calls have 30s timeout (bot.go:938)
- DM sending uses 2min detached context (bot.go:438)
- Graceful shutdown implemented (main.go:59-67)
- Context propagation through stack

---

## ⚠️ Issues Requiring Fixes Before Production

### CRITICAL: No Panic Recovery in Goroutines

**Problem:** Multiple goroutines launched without panic recovery:
1. **bot.go:439-442** - DM notification goroutine (no defer recover)
2. **bot.go:717-720** - Parallel channel processing (no defer recover)
3. **main.go:154-164** - Bot coordinator goroutines (no defer recover)

**Risk:** A single panic in any goroutine will crash the entire server, affecting all workspaces.

**Fix Required:**
```go
// Pattern to add at the start of ALL goroutines:
defer func() {
    if r := recover(); r != nil {
        slog.Error("goroutine panic recovered",
            "panic", r,
            "stack", string(debug.Stack()))
    }
}()
```

**Files to fix:**
- bot.go:439 (sendDMNotifications goroutine)
- bot.go:717 (processPRForChannel goroutines)
- bot_sprinkler.go:166 (OnEvent callback - implicit goroutine)
- main.go:290 (coordinator goroutines)

### HIGH: Memory Leak in NotificationTracker

**Problem:** notify/tracker.go stores unbounded maps that never cleanup:
```go
lastDM                  map[string]time.Time  // Never cleared
lastDaily               map[string]time.Time  // Never cleared
lastChannelNotification map[string]time.Time  // Never cleared
lastUserPRChannelTag    map[string]TagInfo    // Never cleared
```

**Risk:** Memory grows indefinitely as PRs are created. With 100 PRs/day across 10 workspaces = 36,500 entries/year.

**Fix Options:**

**Option 1: TTL-based cleanup (recommended):**
```go
// Add periodic cleanup in notify.Manager.Run()
cleanupTicker := time.NewTicker(1 * time.Hour)
defer cleanupTicker.Stop()

for {
    select {
    case <-cleanupTicker.C:
        m.Tracker.Cleanup(7 * 24 * time.Hour) // Remove entries >7 days old
    }
}
```

**Option 2: LRU cache with size limit:**
Replace maps with `container/list` + map for LRU eviction when size exceeds limit.

### HIGH: ThreadCache Memory Leak

**Problem:** bot.go:33-58 ThreadCache never cleans up old PRs:
```go
type ThreadCache struct {
    prThreads map[string]ThreadInfo // "owner/repo#123" -> thread info
    mu        sync.RWMutex
}
```

**Risk:** Cache grows unbounded. Merged/closed PRs stay in memory forever.

**Fix:** Add TTL or cleanup logic:
```go
type ThreadInfo struct {
    ThreadTS  string    `json:"thread_ts"`
    ChannelID string    `json:"channel_id"`
    LastState string    `json:"last_state"`
    UpdatedAt time.Time `json:"updated_at"` // Add this
}

// Add cleanup method
func (tc *ThreadCache) Cleanup(maxAge time.Duration) {
    tc.mu.Lock()
    defer tc.mu.Unlock()

    cutoff := time.Now().Add(-maxAge)
    for key, info := range tc.prThreads {
        if info.UpdatedAt.Before(cutoff) {
            delete(tc.prThreads, key)
        }
    }
}
```

### MEDIUM: apiCache Memory Leak

**Problem:** slack.go:38-43 cache entries expire but never deleted:
```go
type apiCache struct {
    entries map[string]cacheEntry  // Expired entries accumulate
    mu      sync.RWMutex
}
```

**Fix:** Add cleanup in get():
```go
func (c *apiCache) get(key string) (any, bool) {
    c.mu.Lock()  // Changed from RLock - we might delete
    defer c.mu.Unlock()

    entry, exists := c.entries[key]
    if !exists {
        c.misses++
        return nil, false
    }
    if time.Now().After(entry.expiresAt) {
        delete(c.entries, key)  // Clean up expired entry
        c.misses++
        return nil, false
    }
    c.hits++
    return entry.value, true
}
```

### MEDIUM: No Rate Limiting on OAuth Endpoints

**Problem:** cmd/slack-registrar/main.go:54-57 OAuth endpoints have no rate limiting:
```go
router.HandleFunc("/install", oauthHandler.HandleInstall).Methods("GET")
router.HandleFunc("/oauth/callback", oauthHandler.HandleCallback).Methods("GET")
```

**Risk:** Abuse/DDoS attacks on installation flow.

**Fix:** Add rate limiting middleware:
```go
import "golang.org/x/time/rate"

func rateLimitMiddleware(limiter *rate.Limiter) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if !limiter.Allow() {
                http.Error(w, "Too many requests", http.StatusTooManyRequests)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

// Apply to OAuth routes only
oauthLimiter := rate.NewLimiter(10, 20) // 10 req/s, burst 20
router.Handle("/install", rateLimitMiddleware(oauthLimiter)(oauthHandler.HandleInstall))
```

---

## 🔧 Recommended Improvements (Not Blockers)

### 1. Add Observability

**Metrics to add:**
```go
// Using prometheus
var (
    prProcessed = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "r2r_prs_processed_total",
        },
        []string{"org", "repo", "state"},
    )

    dmsSent = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "r2r_dms_sent_total",
        },
        []string{"workspace", "reason"},
    )

    apiLatency = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "r2r_api_latency_seconds",
        },
        []string{"service", "method", "status"},
    )
)
```

**Add `/metrics` endpoint in main.go:**
```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

router.Handle("/metrics", promhttp.Handler())
```

### 2. Add Circuit Breakers

**For external dependencies:**
```go
import "github.com/sony/gobreaker"

// Add to Client struct
type Client struct {
    api           *slack.Client
    cache         *apiCache
    signingSecret string
    breaker       *gobreaker.CircuitBreaker  // Add this
}

// Initialize in New()
func New(token, signingSecret string) *Client {
    return &Client{
        api:           slack.New(token),
        signingSecret: signingSecret,
        cache: &apiCache{
            entries: make(map[string]cacheEntry),
        },
        breaker: gobreaker.NewCircuitBreaker(gobreaker.Settings{
            Name:        "slack-api",
            MaxRequests: 10,
            Interval:    60 * time.Second,
            Timeout:     30 * time.Second,
        }),
    }
}
```

### 3. Add Structured Errors

**Current:** Errors are wrapped with fmt.Errorf
**Better:** Use custom error types:
```go
type APIError struct {
    Service string
    Method  string
    Code    int
    Message string
    Err     error
}

func (e *APIError) Error() string {
    return fmt.Sprintf("%s.%s: %s (code=%d): %v",
        e.Service, e.Method, e.Message, e.Code, e.Err)
}

func (e *APIError) Unwrap() error {
    return e.Err
}
```

### 4. Add Health Check Improvements

**Current:** main.go:544-565 basic health check
**Add:** Detailed health with dependencies:
```go
type HealthStatus struct {
    Status        string            `json:"status"`
    Version       string            `json:"version"`
    Uptime        string            `json:"uptime"`
    Coordinators  int               `json:"coordinators"`
    Dependencies  map[string]string `json:"dependencies"`
}

func makeDetailedHealthz(githubManager *github.Manager) http.HandlerFunc {
    startTime := time.Now()
    return func(w http.ResponseWriter, r *http.Request) {
        status := HealthStatus{
            Status:       "healthy",
            Version:      "1.0.0", // From build
            Uptime:       time.Since(startTime).String(),
            Coordinators: len(githubManager.AllOrgs()),
            Dependencies: map[string]string{
                "github":    "ok",
                "slack":     "ok",
                "sprinkler": "ok",
            },
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(status)
    }
}
```

### 5. Add Graceful Degradation

**When Slack is down:**
```go
// In notify.Manager - queue failed DMs for retry
type DMQueue struct {
    mu      sync.Mutex
    pending []PendingDM
}

type PendingDM struct {
    WorkspaceID string
    UserID      string
    PRInfo      PRInfo
    Attempts    int
    NextRetry   time.Time
}
```

### 6. Add Request ID Tracing

**For debugging across services:**
```go
// Middleware to add request IDs
func requestIDMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        requestID := r.Header.Get("X-Request-ID")
        if requestID == "" {
            requestID = generateRequestID()
        }
        ctx := context.WithValue(r.Context(), "request_id", requestID)
        w.Header().Set("X-Request-ID", requestID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

---

## 📊 Performance Considerations

### Current Performance is Good:
1. ✅ Parallel channel processing (bot.go:711-728)
2. ✅ Async DM sending (bot.go:436-442)
3. ✅ API response caching (slack.go:38-80)
4. ✅ Channel resolution caching (1 hour TTL)
5. ✅ User mapping caching (24 hour TTL in usermapping)

### Potential Bottlenecks:
1. **Channel history search** (bot.go:195-272)
   - Fetches up to 1000 messages per PR
   - Could be slow for busy channels
   - **Fix:** Add pagination limits or time-based cutoff

2. **Turnclient API calls** (bot.go:942)
   - 30s timeout is reasonable
   - But no caching of PR analysis
   - **Fix:** Cache CheckResponse for ~5 mins

3. **User mapping** (usermapping service)
   - Makes GitHub API calls for each user
   - 24h TTL is good
   - No issues found

---

## 🔒 Security Considerations

### ✅ Already Good:
1. Webhook signature verification (slack.go:819-840)
2. HMAC signature validation (slack.go:834)
3. Timestamp replay protection (slack.go:822-828)
4. Security headers middleware (main.go:569-580)
5. Tokens stored in GSM (manager.go:58)
6. No token logging

### Improvements Needed:
1. **Rate limiting on OAuth** (mentioned above)
2. **Input validation** on slash commands (currently basic)
3. **CSRF tokens** in OAuth flow (optional but recommended)

---

## 💾 Data Persistence

### Current State:
- **All state is in-memory** (by design)
- State resets on restart
- ThreadCache, NotificationTracker, apiCache all volatile

### Production Considerations:
This is **acceptable** for the use case because:
1. PRs are re-processed on next webhook event
2. Notification history loss = user might get duplicate DM (minor)
3. Cache rebuild on restart is fast

### Optional Persistence:
If you want persistence:
```go
// Use Redis for:
1. ThreadCache - persist thread mappings
2. NotificationTracker - prevent duplicate DMs across restarts
3. apiCache - share cache across instances
```

---

## 🚀 Deployment Checklist

Before production deployment:

### MUST FIX (P0):
- [ ] Add panic recovery to all goroutines
- [ ] Add cleanup for NotificationTracker memory leak
- [ ] Add cleanup for ThreadCache memory leak
- [ ] Fix apiCache expired entry deletion

### SHOULD FIX (P1):
- [ ] Add rate limiting on OAuth endpoints
- [ ] Add metrics endpoint for monitoring
- [ ] Add detailed health check
- [ ] Add structured logging with request IDs

### NICE TO HAVE (P2):
- [ ] Add circuit breakers
- [ ] Add graceful degradation
- [ ] Cache turnclient responses
- [ ] Add persistent storage option

---

## 🧪 Testing Recommendations

### Load Testing:
```bash
# Test concurrent webhook processing
ab -n 1000 -c 50 -H "Content-Type: application/json" \
   -p webhook.json https://your-domain.com/slack/events

# Test OAuth flow under load
ab -n 100 -c 10 https://your-domain.com/install
```

### Chaos Testing:
1. Kill sprinkler connection → should reconnect
2. Kill Slack API → should retry and queue
3. Kill GitHub API → should retry
4. Restart with 1000 cached PRs → check memory

### Memory Profiling:
```bash
# Add pprof endpoint
import _ "net/http/pprof"

// In main.go
router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

# Profile in production
go tool pprof http://your-domain.com/debug/pprof/heap
```

---

## Summary

**Code Quality:** ⭐⭐⭐⭐ (4/5)
- Excellent error handling and retry logic
- Good concurrency patterns
- Clean architecture

**Production Readiness:** ⚠️ (3/5)
- **Blockers:** Memory leaks and missing panic recovery
- **Once fixed:** Ready for production
- **With improvements:** Enterprise-grade

**Estimated Fix Time:**
- Critical issues: 4-6 hours
- Recommended improvements: 8-12 hours
- Full observability: 16-20 hours

The codebase is **very close** to production-ready. The main gaps are defensive programming (panic recovery) and memory management (cleanup routines). Once those are fixed, it's solid.
