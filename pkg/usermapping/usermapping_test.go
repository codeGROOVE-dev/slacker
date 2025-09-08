package usermapping

import (
	"context"
	"testing"
	"time"

	ghmailto "github.com/codeGROOVE-dev/gh-mailto/pkg/gh-mailto"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockSlackAPI mocks the Slack API for testing.
type MockSlackAPI struct {
	mock.Mock
}

func (m *MockSlackAPI) GetUserByEmailContext(ctx context.Context, email string) (*slack.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*slack.User), args.Error(1)
}

// MockGitHubLookup mocks the GitHub email lookup for testing.
type MockGitHubLookup struct {
	mock.Mock
}

func (m *MockGitHubLookup) Lookup(ctx context.Context, username, organization string) (*ghmailto.Result, error) {
	args := m.Called(ctx, username, organization)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	result, ok := args.Get(0).(*ghmailto.Result)
	if !ok {
		return nil, args.Error(1)
	}
	return result, args.Error(1)
}

func (m *MockGitHubLookup) Guess(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error) {
	args := m.Called(ctx, username, organization, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	result, ok := args.Get(0).(*ghmailto.GuessResult)
	if !ok {
		return nil, args.Error(1)
	}
	return result, args.Error(1)
}

func TestService_GetSlackHandle_Success(t *testing.T) {
	mockSlack := &MockSlackAPI{}
	mockGitHub := &MockGitHubLookup{}
	
	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	ctx := context.Background()
	githubUser := "testuser"
	organization := "testorg"
	domain := "example.com"
	testEmail := "test@example.com"

	// Mock GitHub email lookup
	githubResult := &ghmailto.Result{
		Username: githubUser,
		Addresses: []ghmailto.Address{
			{
				Email:    testEmail,
				Name:     "Test User",
				Methods:  []string{"Public API"},
				Verified: true,
			},
		},
	}
	mockGitHub.On("Lookup", ctx, githubUser, organization).Return(githubResult, nil)

	// Mock Slack user lookup
	slackUser := &slack.User{
		ID:   "U123456",
		Name: "testuser.slack",
		Profile: slack.UserProfile{
			Email: testEmail,
		},
		Deleted: false,
	}
	mockSlack.On("GetUserByEmailContext", ctx, testEmail).Return(slackUser, nil)

	result, err := service.GetSlackHandle(ctx, githubUser, organization, domain)

	assert.NoError(t, err)
	assert.Equal(t, "testuser.slack", result)
	
	// Verify mapping was cached
	cachedMapping := service.getCachedMapping(githubUser)
	if assert.NotNil(t, cachedMapping, "Expected cached mapping to exist") {
		assert.Equal(t, githubUser, cachedMapping.GitHubUsername)
		assert.Equal(t, "testuser.slack", cachedMapping.SlackUsername)
		assert.Equal(t, "U123456", cachedMapping.SlackUserID)
		assert.Greater(t, cachedMapping.Confidence, 50) // Should have high confidence
	}

	mockSlack.AssertExpectations(t)
	mockGitHub.AssertExpectations(t)
}

func TestService_GetSlackHandle_FallbackToGitHub(t *testing.T) {
	mockSlack := &MockSlackAPI{}
	mockGitHub := &MockGitHubLookup{}
	
	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	ctx := context.Background()
	githubUser := "unknownuser"
	organization := "testorg"
	domain := "example.com"

	// Mock GitHub returning no emails
	githubResult := &ghmailto.Result{
		Username:  githubUser,
		Addresses: []ghmailto.Address{}, // No emails found
	}
	mockGitHub.On("Lookup", ctx, githubUser, organization).Return(githubResult, nil)
	
	// Mock the Guess method in case it gets called
	emptyGuessResult := &ghmailto.GuessResult{
		Username: githubUser,
		Guesses:  []string{},
		FoundAddresses: []ghmailto.Address{},
	}
	mockGitHub.On("Guess", mock.Anything, githubUser, organization, mock.Anything).Return(emptyGuessResult, nil)

	result, err := service.GetSlackHandle(ctx, githubUser, organization, domain)

	assert.NoError(t, err)
	assert.Equal(t, "", result) // No mapping found

	// Verify negative result was cached
	cachedMapping := service.getCachedMapping(githubUser)
	assert.NotNil(t, cachedMapping)
	assert.Equal(t, "", cachedMapping.SlackUsername)
	assert.Equal(t, 0, cachedMapping.Confidence)

	mockGitHub.AssertExpectations(t)
}

func TestService_FormatUserMention_WithMapping(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	// Pre-populate cache with mapping
	service.cacheMapping(&UserMapping{
		GitHubUsername: "testuser",
		SlackUserID:    "U123456",
		SlackUsername:  "testuser.slack",
		Confidence:     90,
		CachedAt:       time.Now(),
	})

	ctx := context.Background()
	organization := "testorg"
	domain := "example.com"
	result := service.FormatUserMention(ctx, "testuser", organization, domain)

	assert.Equal(t, "<@testuser.slack>", result)
}

func TestService_FormatUserMention_NoMapping(t *testing.T) {
	mockSlack := &MockSlackAPI{}
	mockGitHub := &MockGitHubLookup{}
	
	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	// Mock GitHub returning no emails
	githubResult := &ghmailto.Result{
		Username:  "unknownuser",
		Addresses: []ghmailto.Address{},
	}

	ctx := context.Background()
	organization := "testorg"
	domain := "example.com"
	mockGitHub.On("Lookup", mock.Anything, "unknownuser", organization).Return(githubResult, nil)
	
	// Mock the Guess method that will be called when no emails are found but domain is specified
	guessResult := &ghmailto.GuessResult{
		Username: "unknownuser",
		Guesses:  []string{}, // No guesses either
		FoundAddresses: []ghmailto.Address{},
	}
	mockGitHub.On("Guess", mock.Anything, "unknownuser", organization, ghmailto.GuessOptions{Domain: domain}).Return(guessResult, nil)
	result := service.FormatUserMention(ctx, "unknownuser", organization, domain)

	assert.Equal(t, "@unknownuser", result) // Fallback to GitHub username
}

func TestService_FormatUserMentions_Mixed(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	// Pre-populate cache with mappings
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

	ctx := context.Background()
	users := []string{"user1", "user2", "user3"} // user3 has no mapping

	// Mock GitHub lookup for user3
	mockSlack := &MockSlackAPI{}
	mockGitHub := &MockGitHubLookup{}
	service.slackClient = mockSlack
	service.githubLookup = mockGitHub
	service.lookupSem = make(chan struct{}, 5)

	githubResult := &ghmailto.Result{
		Username:  "user3",
		Addresses: []ghmailto.Address{},
	}
	organization := "testorg"
	domain := "example.com"
	mockGitHub.On("Lookup", mock.Anything, "user3", organization).Return(githubResult, nil)
	
	// Mock the Guess method in case it gets called
	emptyGuessResult := &ghmailto.GuessResult{
		Username: "user3",
		Guesses:  []string{},
		FoundAddresses: []ghmailto.Address{},
	}
	mockGitHub.On("Guess", mock.Anything, "user3", organization, mock.Anything).Return(emptyGuessResult, nil)

	result := service.FormatUserMentions(ctx, users, organization, domain)

	// Should return mixed format: Slack handles for mapped users, GitHub usernames for unmapped
	assert.Contains(t, result, "<@user1.slack>")
	assert.Contains(t, result, "<@user2.slack>")
	assert.Contains(t, result, "@user3") // Fallback
}

func TestService_CacheExpiration(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	// Add expired mapping
	expiredMapping := &UserMapping{
		GitHubUsername: "olduser",
		SlackUsername:  "old.slack",
		CachedAt:       time.Now().Add(-25 * time.Hour), // Expired (older than 24h)
	}
	service.cacheMapping(expiredMapping)

	// Add fresh mapping
	freshMapping := &UserMapping{
		GitHubUsername: "newuser",
		SlackUsername:  "new.slack", 
		CachedAt:       time.Now(),
	}
	service.cacheMapping(freshMapping)

	// Check that expired mapping returns nil
	result := service.getCachedMapping("olduser")
	assert.Nil(t, result)

	// Check that fresh mapping is returned
	result = service.getCachedMapping("newuser")
	assert.NotNil(t, result)
	assert.Equal(t, "new.slack", result.SlackUsername)
}

func TestService_MultipleEmailMatches(t *testing.T) {
	mockSlack := &MockSlackAPI{}
	mockGitHub := &MockGitHubLookup{}
	
	service := &Service{
		slackClient:  mockSlack,
		githubLookup: mockGitHub,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, 5),
	}

	ctx := context.Background()
	githubUser := "testuser"
	organization := "testorg" 
	domain := "example.com"
	email1 := "test1@example.com"
	email2 := "test2@example.com"

	// Mock GitHub returning multiple emails
	githubResult := &ghmailto.Result{
		Username: githubUser,
		Addresses: []ghmailto.Address{
			{Email: email1, Name: "Test User 1", Methods: []string{"Public API"}, Verified: false},
			{Email: email2, Name: "Test User 2", Methods: []string{"Commits"}, Verified: false},
		},
	}
	mockGitHub.On("Lookup", ctx, githubUser, organization).Return(githubResult, nil)

	// Mock Slack users - user1 has primary email, user2 has secondary email
	slackUser1 := &slack.User{ID: "U111111", Name: "user1", Profile: slack.UserProfile{Email: email1}, Deleted: false}
	slackUser2 := &slack.User{ID: "U222222", Name: "user2", Profile: slack.UserProfile{Email: "different@example.com"}, Deleted: false}
	
	mockSlack.On("GetUserByEmailContext", ctx, email1).Return(slackUser1, nil)
	mockSlack.On("GetUserByEmailContext", ctx, email2).Return(slackUser2, nil)

	result, err := service.GetSlackHandle(ctx, githubUser, organization, domain)

	assert.NoError(t, err)
	assert.Equal(t, "user1", result) // Should prefer user1 because they have the primary email match

	// Verify the mapping was cached
	cachedMapping := service.getCachedMapping(githubUser)
	assert.NotNil(t, cachedMapping)
	assert.Equal(t, githubUser, cachedMapping.GitHubUsername)

	mockSlack.AssertExpectations(t)
	mockGitHub.AssertExpectations(t)
}

func TestService_ConfidenceScoring(t *testing.T) {
	service := &Service{}

	tests := []struct {
		name           string
		user           *slack.User
		email          string
		expectedMin    int
		expectedMax    int
	}{
		{
			name: "primary email match",
			user: &slack.User{
				ID:   "U123",
				Name: "test",
				Profile: slack.UserProfile{Email: "test@example.com"},
			},
			email:          "test@example.com",
			expectedMin:    70, // Base 50 + primary email 20
			expectedMax:    70,
		},
		{
			name: "secondary email",
			user: &slack.User{
				ID:   "U123",
				Name: "test",
				Profile: slack.UserProfile{Email: "primary@example.com"},
			},
			email:          "secondary@example.com",
			expectedMin:    50, // Base 50 only
			expectedMax:    50,
		},
		{
			name: "generic email",
			user: &slack.User{
				ID:   "U123",
				Name: "test", 
				Profile: slack.UserProfile{Email: "noreply@example.com"},
			},
			email:          "noreply@example.com",
			expectedMin:    50, // Base 50 + primary 20 - generic 20
			expectedMax:    50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confidence := service.calculateConfidence(tt.user, tt.email)
			assert.GreaterOrEqual(t, confidence, tt.expectedMin)
			assert.LessOrEqual(t, confidence, tt.expectedMax)
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
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestService_CacheStats(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	// Add some mappings with different ages
	// Note: Due to automatic cleanup, expired entries are removed when new ones are added
	service.cacheMapping(&UserMapping{
		GitHubUsername: "user1",
		CachedAt:       time.Now(),
	})
	
	// Manually add expired entry to bypass cleanup
	service.cache["user2"] = &UserMapping{
		GitHubUsername: "user2",
		CachedAt:       time.Now().Add(-25 * time.Hour), // Expired
	}
	
	service.cacheMapping(&UserMapping{
		GitHubUsername: "user3",
		CachedAt:       time.Now().Add(-1 * time.Hour), // Fresh
	})

	// After the last cacheMapping call, expired entries should be cleaned up
	total, expired := service.CacheStats()
	assert.Equal(t, 2, total) // user1 and user3 (user2 was cleaned up)
	assert.Equal(t, 0, expired) // No expired entries remain
}

func TestService_ClearCache(t *testing.T) {
	service := &Service{
		cache: make(map[string]*UserMapping),
	}

	// Add some mappings
	service.cacheMapping(&UserMapping{GitHubUsername: "user1", CachedAt: time.Now()})
	service.cacheMapping(&UserMapping{GitHubUsername: "user2", CachedAt: time.Now()})

	total, _ := service.CacheStats()
	assert.Equal(t, 2, total)

	service.ClearCache()

	total, _ = service.CacheStats()
	assert.Equal(t, 0, total)
}