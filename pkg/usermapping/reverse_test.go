package usermapping

import (
	"context"
	"testing"
	"time"

	ghmailto "github.com/codeGROOVE-dev/gh-mailto/pkg/gh-mailto"
	"github.com/slack-go/slack"
)

// mockSlackClient implements SlackAPI for testing.
type mockSlackClient struct {
	users map[string]*slack.User
}

func (m *mockSlackClient) GetUserByEmailContext(_ context.Context, email string) (*slack.User, error) {
	for _, user := range m.users {
		if user.Profile.Email == email {
			return user, nil
		}
	}
	return nil, &slack.SlackErrorResponse{Err: "user_not_found"}
}

func (m *mockSlackClient) GetUserInfo(userID string) (*slack.User, error) {
	if user, exists := m.users[userID]; exists {
		return user, nil
	}
	return nil, &slack.SlackErrorResponse{Err: "user_not_found"}
}

// mockOrgCache implements the org cache for testing.
func createMockOrgCache(org string, identities []ghmailto.OrgIdentity) *ghmailto.OrgIdentityCache {
	cache := &ghmailto.OrgIdentityCache{
		Organization:  org,
		CachedAt:      time.Now(),
		Identities:    identities,
		EmailToGitHub: make(map[string]string),
		GitHubToEmail: make(map[string]string),
		TotalMembers:  len(identities),
	}

	for i := range identities {
		identity := &identities[i]
		if identity.PrimaryEmail != "" {
			cache.GitHubToEmail[identity.GitHubUsername] = identity.PrimaryEmail
			cache.EmailToGitHub[identity.PrimaryEmail] = identity.GitHubUsername
		}
	}

	return cache
}

func TestReverseMapping_ConfigOverride(t *testing.T) {
	mockSlack := &mockSlackClient{
		users: map[string]*slack.User{
			"U12345": {
				ID:   "U12345",
				Name: "testuser",
				Profile: slack.UserProfile{
					Email: "test@company.com",
				},
			},
		},
	}

	service := NewReverseService(nil, "fake-token")
	service.SetOverrides(map[string]string{
		"githubuser": "test@company.com",
	})

	ctx := context.Background()
	mapping, err := service.LookupGitHub(ctx, mockSlack, "U12345", "test-org", "company.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if mapping.GitHubUsername != "githubuser" {
		t.Errorf("expected github username 'githubuser', got: %s", mapping.GitHubUsername)
	}

	if mapping.MatchMethod != "config_override" {
		t.Errorf("expected match method 'config_override', got: %s", mapping.MatchMethod)
	}

	if mapping.Confidence != 100 {
		t.Errorf("expected confidence 100, got: %d", mapping.Confidence)
	}
}

func TestReverseMapping_CacheHit(t *testing.T) {
	mockSlack := &mockSlackClient{
		users: map[string]*slack.User{
			"U12345": {
				ID:   "U12345",
				Name: "testuser",
				Profile: slack.UserProfile{
					Email: "test@company.com",
				},
			},
		},
	}

	service := NewReverseService(nil, "fake-token")

	// Populate cache manually
	service.cache["U12345"] = &ReverseMapping{
		CachedAt:       time.Now(),
		SlackUserID:    "U12345",
		SlackUsername:  "testuser",
		SlackEmail:     "test@company.com",
		GitHubUsername: "cached-user",
		MatchMethod:    "cached",
		Confidence:     90,
	}

	ctx := context.Background()
	mapping, err := service.LookupGitHub(ctx, mockSlack, "U12345", "test-org", "company.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if mapping.GitHubUsername != "cached-user" {
		t.Errorf("expected cached username 'cached-user', got: %s", mapping.GitHubUsername)
	}

	if mapping.MatchMethod != "cached" {
		t.Errorf("expected match method 'cached', got: %s", mapping.MatchMethod)
	}
}

func TestReverseMapping_CacheExpiry(t *testing.T) {
	service := NewReverseService(nil, "fake-token")

	// Populate cache with expired entry
	service.cache["U12345"] = &ReverseMapping{
		CachedAt:       time.Now().Add(-25 * time.Hour), // Expired (>24h)
		SlackUserID:    "U12345",
		GitHubUsername: "expired-user",
	}

	// Should return nil for expired cache
	mapping := service.cachedMapping("U12345")
	if mapping != nil {
		t.Errorf("expected nil for expired cache, got: %v", mapping)
	}
}

func TestReverseMapping_CacheStats(t *testing.T) {
	service := NewReverseService(nil, "fake-token")

	// Add some cache entries
	service.cache["U1"] = &ReverseMapping{
		CachedAt:    time.Now(),
		SlackUserID: "U1",
	}
	service.cache["U2"] = &ReverseMapping{
		CachedAt:    time.Now().Add(-25 * time.Hour), // Expired
		SlackUserID: "U2",
	}
	service.cache["U3"] = &ReverseMapping{
		CachedAt:    time.Now(),
		SlackUserID: "U3",
	}

	total, expired := service.CacheStats()
	if total != 3 {
		t.Errorf("expected total 3, got: %d", total)
	}
	if expired != 1 {
		t.Errorf("expected expired 1, got: %d", expired)
	}
}

func TestReverseMapping_ClearCache(t *testing.T) {
	service := NewReverseService(nil, "fake-token")

	// Add cache entries
	service.cache["U1"] = &ReverseMapping{SlackUserID: "U1"}
	service.cache["U2"] = &ReverseMapping{SlackUserID: "U2"}

	service.ClearCache()

	total, _ := service.CacheStats()
	if total != 0 {
		t.Errorf("expected cache to be empty after clear, got: %d entries", total)
	}
}

func TestReverseMapping_SlackUserNotFound(t *testing.T) {
	mockSlack := &mockSlackClient{
		users: map[string]*slack.User{},
	}

	service := NewReverseService(nil, "fake-token")

	ctx := context.Background()
	_, err := service.LookupGitHub(ctx, mockSlack, "U99999", "test-org", "company.com")
	if err == nil {
		t.Fatal("expected error for non-existent Slack user, got nil")
	}
}

func TestReverseMapping_NoEmail(t *testing.T) {
	mockSlack := &mockSlackClient{
		users: map[string]*slack.User{
			"U12345": {
				ID:   "U12345",
				Name: "testuser",
				Profile: slack.UserProfile{
					Email: "", // No email
				},
			},
		},
	}

	service := NewReverseService(nil, "fake-token")

	ctx := context.Background()
	_, err := service.LookupGitHub(ctx, mockSlack, "U12345", "test-org", "company.com")
	if err == nil {
		t.Fatal("expected error for user with no email, got nil")
	}
}

func TestReverseMapping_SetOverrides(t *testing.T) {
	service := NewReverseService(nil, "fake-token")

	overrides := map[string]string{
		"user1": "user1@example.com",
		"user2": "user2@example.com",
	}

	service.SetOverrides(overrides)

	if len(service.overrides) != 2 {
		t.Errorf("expected 2 overrides, got: %d", len(service.overrides))
	}

	if service.overrides["user1"] != "user1@example.com" {
		t.Errorf("expected override for user1, got: %s", service.overrides["user1"])
	}
}
