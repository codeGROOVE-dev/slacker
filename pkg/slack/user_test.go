package slack

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestUserInfo(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		api := &mockAPI{
			getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
				return &slack.User{
					ID:   userID,
					Name: "testuser",
				}, nil
			},
		}

		client := &Client{
			api: api,
		}

		user, err := client.UserInfo(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if user.ID != "U123" {
			t.Errorf("expected user ID U123, got %s", user.ID)
		}
	})

	t.Run("user_not_found", func(t *testing.T) {
		api := &mockAPI{
			getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
				return nil, errors.New("user_not_found")
			},
		}

		client := &Client{
			api: api,
		}

		_, err := client.UserInfo(ctx, "U999")
		if err == nil {
			t.Fatal("expected error for non-existent user")
		}
	})
}

func TestUserPresence(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("active", func(t *testing.T) {
		api := &mockAPI{
			getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
				return &slack.UserPresence{
					Presence: "active",
				}, nil
			},
		}

		client := &Client{
			api: api,
		}

		presence, err := client.UserPresence(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if presence != "active" {
			t.Errorf("expected active, got %s", presence)
		}
	})

	t.Run("away", func(t *testing.T) {
		api := &mockAPI{
			getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
				return &slack.UserPresence{
					Presence: "away",
				}, nil
			},
		}

		client := &Client{
			api: api,
		}

		presence, err := client.UserPresence(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if presence != "away" {
			t.Errorf("expected away, got %s", presence)
		}
	})

	t.Run("error", func(t *testing.T) {
		api := &mockAPI{
			getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
				return nil, errors.New("user_not_found")
			},
		}

		client := &Client{
			api: api,
		}

		_, err := client.UserPresence(ctx, "U999")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	//nolint:tparallel // Tests share resources, cannot run subtests in parallel
}

func TestIsUserActive(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("active", func(t *testing.T) {
		api := &mockAPI{
			getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
				return &slack.UserPresence{
					Presence: "active",
				}, nil
			},
		}

		client := &Client{
			api: api,
		}

		if !client.IsUserActive(ctx, "U123") {
			t.Error("expected user to be active")
		}
	})

	t.Run("away", func(t *testing.T) {
		api := &mockAPI{
			getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
				return &slack.UserPresence{
					Presence: "away",
				}, nil
			},
		}

		client := &Client{
			api: api,
		}

		if client.IsUserActive(ctx, "U123") {
			t.Error("expected user to be away")
		}
	})

	t.Run("error", func(t *testing.T) {
		api := &mockAPI{
			getUserPresenceFunc: func(ctx context.Context, userID string) (*slack.UserPresence, error) {
				return nil, errors.New("api error")
			},
		}

		client := &Client{
			api:        api,
			retryDelay: 1 * time.Millisecond,
		}

		// Should return false on error
		if client.IsUserActive(ctx, "U123") {
			t.Error("expected false on error")
		}
		//nolint:tparallel // Tests share resources, cannot run subtests in parallel
	})
}

func TestUserTimezone(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("has_timezone", func(t *testing.T) {
		api := &mockAPI{
			getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
				return &slack.User{
					ID: userID,
					TZ: "America/New_York",
				}, nil
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		tz, err := client.UserTimezone(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if tz != "America/New_York" {
			t.Errorf("expected America/New_York, got %s", tz)
		}
	})

	t.Run("no_timezone_defaults_to_utc", func(t *testing.T) {
		api := &mockAPI{
			getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
				return &slack.User{
					ID: userID,
					TZ: "",
				}, nil
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		tz, err := client.UserTimezone(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if tz != "UTC" {
			t.Errorf("expected UTC, got %s", tz)
		}
	})

	t.Run("cached_value", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
				callCount++
				if callCount == 1 {
					return &slack.User{
						ID: userID,
						TZ: "America/New_York",
					}, nil
				}
				return &slack.User{
					ID: userID,
					TZ: "Europe/London",
				}, nil
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		// First call
		tz1, err := client.UserTimezone(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Second call should return cached value
		tz2, err := client.UserTimezone(ctx, "U123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if tz1 != tz2 {
			t.Errorf("expected cached value %s, got %s", tz1, tz2)
		}

		if tz2 != "America/New_York" {
			t.Errorf("expected cached America/New_York, got %s", tz2)
		}

		if callCount != 1 {
			t.Errorf("expected 1 API call due to caching, got %d", callCount)
		}
	})

	t.Run("error", func(t *testing.T) {
		api := &mockAPI{
			getUserInfoFunc: func(ctx context.Context, userID string) (*slack.User, error) {
				return nil, errors.New("api error")
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		_, err := client.UserTimezone(ctx, "U123")
		if err == nil {
			t.Fatal("expected error")
			//nolint:tparallel // Tests share resources, cannot run subtests in parallel
		}
	})
}

func TestWorkspaceInfo(t *testing.T) {
	ctx := context.Background()
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		api := &mockAPI{
			getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
				return &slack.TeamInfo{
					ID:   "T123",
					Name: "Test Workspace",
				}, nil
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		info, err := client.WorkspaceInfo(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.ID != "T123" {
			t.Errorf("expected workspace ID T123, got %s", info.ID)
		}

		if info.Name != "Test Workspace" {
			t.Errorf("expected workspace name 'Test Workspace', got %s", info.Name)
		}
	})

	t.Run("cached_value", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
				callCount++
				if callCount == 1 {
					return &slack.TeamInfo{
						ID:   "T123",
						Name: "Test Workspace",
					}, nil
				}
				return &slack.TeamInfo{
					ID:   "T123",
					Name: "Different Workspace",
				}, nil
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		// First call
		info1, err := client.WorkspaceInfo(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Second call should return cached value
		info2, err := client.WorkspaceInfo(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info1.Name != info2.Name {
			t.Errorf("expected cached name, got different: %s vs %s", info1.Name, info2.Name)
		}

		if info2.Name != "Test Workspace" {
			t.Errorf("expected cached 'Test Workspace', got %s", info2.Name)
		}

		if callCount != 1 {
			t.Errorf("expected 1 API call due to caching, got %d", callCount)
		}
	})

	t.Run("invalidate_and_refresh", func(t *testing.T) {
		callCount := 0
		api := &mockAPI{
			getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
				callCount++
				if callCount == 1 {
					return &slack.TeamInfo{
						ID:   "T123",
						Name: "Test Workspace",
					}, nil
				}
				return &slack.TeamInfo{
					ID:   "T123",
					Name: "Updated Workspace",
				}, nil
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		// First call
		info1, err := client.WorkspaceInfo(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Invalidate cache directly (team_info is the key used by WorkspaceInfo)
		client.cache.invalidate("team_info")

		// Next call should fetch fresh data
		info2, err := client.WorkspaceInfo(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info1.Name == info2.Name {
			t.Error("expected different name after cache invalidation")
		}

		if info2.Name != "Updated Workspace" {
			t.Errorf("expected 'Updated Workspace', got %s", info2.Name)
		}

		if callCount != 2 {
			t.Errorf("expected 2 API calls (before and after invalidation), got %d", callCount)
		}
	})

	t.Run("error", func(t *testing.T) {
		api := &mockAPI{
			getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
				return nil, errors.New("api error")
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		_, err := client.WorkspaceInfo(ctx)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("incorrect_cache_type", func(t *testing.T) {
		api := &mockAPI{
			getTeamInfoFunc: func(ctx context.Context) (*slack.TeamInfo, error) {
				return &slack.TeamInfo{
					ID:   "T456",
					Name: "Fresh Workspace",
				}, nil
			},
		}

		client := &Client{
			api: api,
			cache: &apiCache{
				entries: make(map[string]cacheEntry),
			},
		}

		// Manually poison the cache with wrong type
		client.cache.set("team_info", "wrong_type_string", time.Hour)

		// Should detect wrong type, invalidate cache, and fetch fresh
		info, err := client.WorkspaceInfo(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.Name != "Fresh Workspace" {
			//nolint:tparallel // Tests share resources, cannot run subtests in parallel
			t.Errorf("expected 'Fresh Workspace' after cache invalidation, got %s", info.Name)
		}
	})
}

func TestAPICache(t *testing.T) {
	t.Parallel()

	cache := &apiCache{
		entries: make(map[string]cacheEntry),
	}

	t.Run("set_and_get", func(t *testing.T) {
		cache.set("test_key", "test_value", time.Hour)

		val, exists := cache.get("test_key")
		if !exists {
			t.Fatal("expected key to exist")
		}

		if val != "test_value" {
			t.Errorf("expected 'test_value', got %v", val)
		}
	})

	t.Run("get_nonexistent", func(t *testing.T) {
		_, exists := cache.get("nonexistent_key")
		if exists {
			t.Error("expected key to not exist")
		}
	})

	t.Run("invalidate", func(t *testing.T) {
		cache.set("test_key2", "test_value2", time.Hour)
		cache.invalidate("test_key2")

		_, exists := cache.get("test_key2")
		if exists {
			t.Error("expected key to be invalidated")
		}
	})

	t.Run("expired", func(t *testing.T) {
		cache.set("test_key3", "test_value3", 10*time.Millisecond)
		time.Sleep(20 * time.Millisecond)

		_, exists := cache.get("test_key3")
		if exists {
			t.Error("expected key to be expired")
		}
	})
}
