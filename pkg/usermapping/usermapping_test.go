package usermapping

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	ghmailto "github.com/codeGROOVE-dev/gh-mailto/pkg/gh-mailto"
	"github.com/slack-go/slack"
)

// Sentinel error for mock "not found" responses.
var errMockNotFound = errors.New("mock: not found")

// MockSlackAPI mocks the Slack API for testing.
type MockSlackAPI struct {
	getUserByEmailFunc func(ctx context.Context, email string) (*slack.User, error)
	getUserInfoFunc    func(userID string) (*slack.User, error)
}

func (m *MockSlackAPI) GetUserByEmailContext(ctx context.Context, email string) (*slack.User, error) {
	if m.getUserByEmailFunc != nil {
		return m.getUserByEmailFunc(ctx, email)
	}
	return nil, errMockNotFound
}

func (m *MockSlackAPI) GetUserInfo(userID string) (*slack.User, error) {
	if m.getUserInfoFunc != nil {
		return m.getUserInfoFunc(userID)
	}
	return nil, errMockNotFound
}

// MockGitHubLookup mocks the GitHub email lookup for testing.
type MockGitHubLookup struct {
	lookupFunc func(ctx context.Context, username, organization string) (*ghmailto.Result, error)
	guessFunc  func(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error)
}

func (m *MockGitHubLookup) Lookup(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
	if m.lookupFunc != nil {
		return m.lookupFunc(ctx, username, organization)
	}
	return &ghmailto.Result{Username: username, Addresses: []ghmailto.Address{}}, nil
}

func (m *MockGitHubLookup) Guess(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error) {
	if m.guessFunc != nil {
		return m.guessFunc(ctx, username, organization, opts)
	}
	return &ghmailto.GuessResult{Username: username, Guesses: []ghmailto.Address{}, FoundAddresses: []ghmailto.Address{}}, nil
}

func TestService_GetSlackHandle_Success(t *testing.T) {
	ctx := context.Background()
	githubUser := "testuser"
	organization := "testorg"
	domain := "example.com"
	testEmail := "test@example.com"

	mockGitHub := &MockGitHubLookup{
		lookupFunc: func(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
			return &ghmailto.Result{
				Username: githubUser,
				Addresses: []ghmailto.Address{
					{Email: testEmail, Name: "Test User", Methods: []string{"Public API"}, Verified: true},
				},
			}, nil
		},
	}

	mockSlack := &MockSlackAPI{
		getUserByEmailFunc: func(ctx context.Context, email string) (*slack.User, error) {
			if email == testEmail {
				return &slack.User{
					ID:      "U123456",
					Name:    "testuser.slack",
					Profile: slack.UserProfile{Email: testEmail},
					Deleted: false,
				}, nil
			}
			return nil, errMockNotFound
		},
	}

	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	result, err := service.SlackHandle(ctx, githubUser, organization, domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "U123456" {
		t.Errorf("expected 'U123456', got %q", result)
	}

	cachedMapping := service.getCachedMapping(githubUser)
	if cachedMapping == nil {
		t.Fatal("expected cached mapping to exist")
	}
	if cachedMapping.GitHubUsername != githubUser {
		t.Errorf("expected GitHubUsername %q, got %q", githubUser, cachedMapping.GitHubUsername)
	}
	if cachedMapping.SlackUsername != "testuser.slack" {
		t.Errorf("expected SlackUsername 'testuser.slack', got %q", cachedMapping.SlackUsername)
	}
	if cachedMapping.SlackUserID != "U123456" {
		t.Errorf("expected SlackUserID 'U123456', got %q", cachedMapping.SlackUserID)
	}
	if cachedMapping.Confidence <= 50 {
		t.Errorf("expected confidence > 50, got %d", cachedMapping.Confidence)
	}
}

func TestService_GetSlackHandle_FallbackToGitHub(t *testing.T) {
	ctx := context.Background()
	githubUser := "unknownuser"
	organization := "testorg"
	domain := "example.com"

	service := &Service{
		slackClient:  &MockSlackAPI{},
		githubLookup: &MockGitHubLookup{},
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	result, err := service.SlackHandle(ctx, githubUser, organization, domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}

	cachedMapping := service.getCachedMapping(githubUser)
	if cachedMapping == nil {
		t.Fatal("expected cached mapping to exist")
	}
	if cachedMapping.SlackUsername != "" {
		t.Errorf("expected empty SlackUsername, got %q", cachedMapping.SlackUsername)
	}
	if cachedMapping.Confidence != 0 {
		t.Errorf("expected confidence 0, got %d", cachedMapping.Confidence)
	}
}

func TestService_FormatUserMention_WithMapping(t *testing.T) {
	ctx := context.Background()
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	service.cacheMapping(&UserMapping{
		GitHubUsername: "testuser",
		SlackUserID:    "U123456",
		SlackUsername:  "testuser.slack",
		Confidence:     90,
		CachedAt:       time.Now(),
	})

	result := service.FormatUserMention(ctx, "testuser", "testorg", "example.com")

	if result != "<@U123456>" {
		t.Errorf("expected '<@U123456>', got %q", result)
	}
}

func TestService_FormatUserMention_NoMapping(t *testing.T) {
	ctx := context.Background()
	service := &Service{
		slackClient:  &MockSlackAPI{},
		githubLookup: &MockGitHubLookup{},
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	result := service.FormatUserMention(ctx, "unknownuser", "testorg", "example.com")

	if result != "@unknownuser" {
		t.Errorf("expected '@unknownuser', got %q", result)
	}
}

func TestService_FormatUserMentions_Mixed(t *testing.T) {
	ctx := context.Background()
	service := &Service{
		slackClient:  &MockSlackAPI{},
		githubLookup: &MockGitHubLookup{},
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	service.cacheMapping(&UserMapping{
		GitHubUsername: "user1",
		SlackUserID:    "U111111",
		SlackUsername:  "user1.slack",
		Confidence:     90,
		CachedAt:       time.Now(),
	})

	service.cacheMapping(&UserMapping{
		GitHubUsername: "user2",
		SlackUserID:    "U222222",
		SlackUsername:  "user2.slack",
		Confidence:     70,
		CachedAt:       time.Now(),
	})

	users := []string{"user1", "user2", "user3"}
	result := service.FormatUserMentions(ctx, users, "testorg", "example.com")

	if !strings.Contains(result, "<@U111111>") {
		t.Errorf("expected result to contain '<@U111111>', got %q", result)
	}
	if !strings.Contains(result, "<@U222222>") {
		t.Errorf("expected result to contain '<@U222222>', got %q", result)
	}
	if !strings.Contains(result, "@user3") {
		t.Errorf("expected result to contain '@user3', got %q", result)
	}
}

func TestService_CacheExpiration(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	expiredMapping := &UserMapping{
		GitHubUsername: "olduser",
		SlackUsername:  "old.slack",
		CachedAt:       time.Now().Add(-25 * time.Hour),
	}
	service.cacheMapping(expiredMapping)

	freshMapping := &UserMapping{
		GitHubUsername: "newuser",
		SlackUsername:  "new.slack",
		CachedAt:       time.Now(),
	}
	service.cacheMapping(freshMapping)

	result := service.getCachedMapping("olduser")
	if result != nil {
		t.Errorf("expected nil for expired mapping, got %v", result)
	}

	result = service.getCachedMapping("newuser")
	if result == nil {
		t.Fatal("expected non-nil for fresh mapping")
	}
	if result.SlackUsername != "new.slack" {
		t.Errorf("expected SlackUsername 'new.slack', got %q", result.SlackUsername)
	}
}

func TestService_MultipleEmailMatches(t *testing.T) {
	ctx := context.Background()
	githubUser := "testuser"
	organization := "testorg"
	domain := "example.com"
	email1 := "test1@example.com"
	email2 := "test2@example.com"

	mockGitHub := &MockGitHubLookup{
		lookupFunc: func(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
			return &ghmailto.Result{
				Username: githubUser,
				Addresses: []ghmailto.Address{
					{Email: email1, Name: "Test User 1", Methods: []string{"Public API"}, Verified: false},
					{Email: email2, Name: "Test User 2", Methods: []string{"Commits"}, Verified: false},
				},
			}, nil
		},
	}

	mockSlack := &MockSlackAPI{
		getUserByEmailFunc: func(ctx context.Context, email string) (*slack.User, error) {
			if email == email1 {
				return &slack.User{
					ID:      "U111111",
					Name:    "user1",
					Profile: slack.UserProfile{Email: email1},
					Deleted: false,
				}, nil
			}
			if email == email2 {
				return &slack.User{
					ID:      "U222222",
					Name:    "user2",
					Profile: slack.UserProfile{Email: "different@example.com"},
					Deleted: false,
				}, nil
			}
			return nil, errMockNotFound
		},
	}

	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	result, err := service.SlackHandle(ctx, githubUser, organization, domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "U111111" {
		t.Errorf("expected 'U111111', got %q", result)
	}

	cachedMapping := service.getCachedMapping(githubUser)
	if cachedMapping == nil {
		t.Fatal("expected cached mapping to exist")
	}
	if cachedMapping.GitHubUsername != githubUser {
		t.Errorf("expected GitHubUsername %q, got %q", githubUser, cachedMapping.GitHubUsername)
	}
}

func TestService_ConfidenceScoring(t *testing.T) {
	service := &Service{}

	tests := []struct {
		name        string
		user        *slack.User
		email       string
		expectedMin int
		expectedMax int
	}{
		{
			name: "primary email match",
			user: &slack.User{
				ID:      "U123",
				Name:    "test",
				Profile: slack.UserProfile{Email: "test@example.com"},
			},
			email:       "test@example.com",
			expectedMin: 70,
			expectedMax: 70,
		},
		{
			name: "secondary email",
			user: &slack.User{
				ID:      "U123",
				Name:    "test",
				Profile: slack.UserProfile{Email: "primary@example.com"},
			},
			email:       "secondary@example.com",
			expectedMin: 50,
			expectedMax: 50,
		},
		{
			name: "generic email",
			user: &slack.User{
				ID:      "U123",
				Name:    "test",
				Profile: slack.UserProfile{Email: "noreply@example.com"},
			},
			email:       "noreply@example.com",
			expectedMin: 50,
			expectedMax: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confidence := service.calculateConfidence(tt.user, tt.email)
			if confidence < tt.expectedMin {
				t.Errorf("expected confidence >= %d, got %d", tt.expectedMin, confidence)
			}
			if confidence > tt.expectedMax {
				t.Errorf("expected confidence <= %d, got %d", tt.expectedMax, confidence)
			}
		})
	}
}

func TestService_GenericEmailDetection(t *testing.T) {
	tests := []struct {
		email    string
		expected bool
	}{
		{"noreply@example.com", true},
		{"no-reply@example.com", true},
		{"donotreply@example.com", true},
		{"admin@example.com", true},
		{"info@example.com", true},
		{"support@example.com", true},
		{"help@example.com", true},
		{"contact@example.com", true},
		{"team@example.com", true},
		{"hello@example.com", true},
		{"hi@example.com", true},
		{"bot@example.com", true},
		{"system@example.com", true},
		{"user@example.com", false},
		{"john.doe@example.com", false},
		{"test123@example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			result := isGenericEmail(tt.email)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestService_CacheStats(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	service.cacheMapping(&UserMapping{
		GitHubUsername: "user1",
		CachedAt:       time.Now(),
	})

	service.cache["user2"] = &UserMapping{
		GitHubUsername: "user2",
		CachedAt:       time.Now().Add(-25 * time.Hour),
	}

	service.cacheMapping(&UserMapping{
		GitHubUsername: "user3",
		CachedAt:       time.Now().Add(-1 * time.Hour),
	})

	total, expired := service.CacheStats()
	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if expired != 0 {
		t.Errorf("expected expired 0, got %d", expired)
	}
}

func TestService_ClearCache(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	service.cacheMapping(&UserMapping{GitHubUsername: "user1", CachedAt: time.Now()})
	service.cacheMapping(&UserMapping{GitHubUsername: "user2", CachedAt: time.Now()})

	total, _ := service.CacheStats()
	if total != 2 {
		t.Errorf("expected total 2 before clear, got %d", total)
	}

	service.ClearCache()

	total, _ = service.CacheStats()
	if total != 0 {
		t.Errorf("expected total 0 after clear, got %d", total)
	}
}

// Test New constructor
func TestNew(t *testing.T) {
	client := &slack.Client{}
	token := "test-token"

	service := New(client, token)

	if service == nil {
		t.Fatal("expected non-nil service")
	}
	if service.slackClient == nil {
		t.Error("expected non-nil slackClient")
	}
	if service.githubLookup == nil {
		t.Error("expected non-nil githubLookup")
	}
	if service.cache == nil {
		t.Error("expected non-nil cache")
	}
	if service.lookupSem == nil {
		t.Error("expected non-nil lookupSem")
	}
}

// Test NewForTesting
func TestNewForTesting(t *testing.T) {
	mockSlack := &MockSlackAPI{}
	mockGitHub := &MockGitHubLookup{}

	service := NewForTesting(mockSlack, mockGitHub)

	if service == nil {
		t.Fatal("expected non-nil service")
	}
	if service.slackClient != mockSlack {
		t.Error("expected mockSlack as slackClient")
	}
	if service.githubLookup != mockGitHub {
		t.Error("expected mockGitHub as githubLookup")
	}
	if service.cache == nil {
		t.Error("expected non-nil cache")
	}
}

// Test SetSlackClient
func TestSetSlackClient(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	mockSlack := &MockSlackAPI{}
	service.SetSlackClient(mockSlack)

	if service.slackClient != mockSlack {
		t.Error("expected slackClient to be set to mockSlack")
	}
}

// Test SetGitHubLookup
func TestSetGitHubLookup(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	mockGitHub := &MockGitHubLookup{}
	service.SetGitHubLookup(mockGitHub)

	if service.githubLookup != mockGitHub {
		t.Error("expected githubLookup to be set to mockGitHub")
	}
}

// Test SlackHandle with guessing
func TestService_SlackHandle_WithGuessing(t *testing.T) {
	ctx := context.Background()
	githubUser := "testuser"
	organization := "testorg"
	domain := "example.com"
	guessedEmail := "testuser@example.com"

	mockGitHub := &MockGitHubLookup{
		lookupFunc: func(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
			return &ghmailto.Result{
				Username:  githubUser,
				Addresses: []ghmailto.Address{},
			}, nil
		},
		guessFunc: func(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error) {
			return &ghmailto.GuessResult{
				Username:       githubUser,
				FoundAddresses: []ghmailto.Address{},
				Guesses: []ghmailto.Address{
					{Email: guessedEmail, Name: "Guess", Methods: []string{"guess"}, Verified: false},
				},
			}, nil
		},
	}

	mockSlack := &MockSlackAPI{
		getUserByEmailFunc: func(ctx context.Context, email string) (*slack.User, error) {
			if email == guessedEmail {
				return &slack.User{
					ID:      "U789",
					Name:    "testuser",
					Profile: slack.UserProfile{Email: guessedEmail},
				}, nil
			}
			return nil, errMockNotFound
		},
	}

	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	result, err := service.SlackHandle(ctx, githubUser, organization, domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "U789" {
		t.Errorf("expected 'U789', got %q", result)
	}
}

// Test SlackHandles for batch operations
func TestService_SlackHandles(t *testing.T) {
	ctx := context.Background()
	organization := "testorg"
	domain := "example.com"

	mockGitHub := &MockGitHubLookup{
		lookupFunc: func(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
			return &ghmailto.Result{
				Username: username,
				Addresses: []ghmailto.Address{
					{Email: username + "@example.com", Verified: true, Methods: []string{"test"}},
				},
			}, nil
		},
	}

	mockSlack := &MockSlackAPI{
		getUserByEmailFunc: func(ctx context.Context, email string) (*slack.User, error) {
			if email != "" && strings.Contains(email, "@example.com") {
				username := strings.Split(email, "@")[0]
				return &slack.User{
					ID:      "U" + strings.ToUpper(username[:min(1, len(username))]),
					Name:    username,
					Profile: slack.UserProfile{Email: email},
				}, nil
			}
			return nil, errMockNotFound
		},
	}

	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	users := []string{"user1", "user2", "user3"}
	results, err := service.SlackHandles(ctx, users, organization, domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// Verify we got valid user IDs
	for user, slackID := range results {
		if !strings.HasPrefix(slackID, "U") {
			t.Errorf("expected Slack ID for %s to start with 'U', got %q", user, slackID)
		}
	}
}

// Test selectBestMatch with various scenarios
func TestSelectBestMatch(t *testing.T) {
	service := &Service{}

	tests := []struct {
		name     string
		matches  []*UserMapping
		expected string
	}{
		{
			name: "single match",
			matches: []*UserMapping{
				{SlackUserID: "U1", GitHubUsername: "test", Confidence: 80},
			},
			expected: "U1",
		},
		{
			name: "highest confidence wins",
			matches: []*UserMapping{
				{SlackUserID: "U1", GitHubUsername: "test1", Confidence: 60},
				{SlackUserID: "U2", GitHubUsername: "test2", Confidence: 90},
				{SlackUserID: "U3", GitHubUsername: "test3", Confidence: 70},
			},
			expected: "U2",
		},
		{
			name:     "no matches",
			matches:  []*UserMapping{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.selectBestMatch(tt.matches)
			if result == nil && tt.expected != "" {
				t.Errorf("expected match with ID %q, got nil", tt.expected)
			} else if result != nil && result.SlackUserID != tt.expected {
				t.Errorf("expected ID %q, got %q", tt.expected, result.SlackUserID)
			}
		})
	}
}

func TestService_SlackHandles_EmptyList(t *testing.T) {
	ctx := context.Background()
	service := &Service{
		slackClient:  &MockSlackAPI{},
		githubLookup: &MockGitHubLookup{},
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	result, err := service.SlackHandles(ctx, []string{}, "testorg", "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d entries", len(result))
	}
}

func TestService_FormatUserMentions_Empty(t *testing.T) {
	ctx := context.Background()
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	result := service.FormatUserMentions(ctx, []string{}, "testorg", "example.com")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestService_ContextCancellation(t *testing.T) {
	service := &Service{
		slackClient:  &MockSlackAPI{},
		githubLookup: &MockGitHubLookup{},
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 1),
	}

	// Fill the semaphore
	service.lookupSem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := service.SlackHandle(ctx, "testuser", "testorg", "example.com")
	if err == nil {
		t.Error("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}

func TestService_EmailGuessing(t *testing.T) {
	ctx := context.Background()
	githubUser := "newuser"
	organization := "testorg"
	domain := "example.com"
	guessedEmail := "newuser@example.com"

	mockGitHub := &MockGitHubLookup{
		lookupFunc: func(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
			// No addresses found via lookup
			return &ghmailto.Result{
				Username:  githubUser,
				Addresses: []ghmailto.Address{},
			}, nil
		},
		guessFunc: func(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error) {
			// Return guessed email
			return &ghmailto.GuessResult{
				Username: githubUser,
				Guesses: []ghmailto.Address{
					{Email: guessedEmail, Confidence: 80, Pattern: "{first}.{last}"},
				},
			}, nil
		},
	}

	mockSlack := &MockSlackAPI{
		getUserByEmailFunc: func(ctx context.Context, email string) (*slack.User, error) {
			if email == guessedEmail {
				return &slack.User{
					ID:      "U999999",
					Name:    "newuser.slack",
					Profile: slack.UserProfile{Email: guessedEmail},
					Deleted: false,
				}, nil
			}
			return nil, errMockNotFound
		},
	}

	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	result, err := service.SlackHandle(ctx, githubUser, organization, domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "U999999" {
		t.Errorf("expected user ID 'U999999', got %q", result)
	}
}
