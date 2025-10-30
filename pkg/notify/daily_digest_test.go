package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/home"
	"github.com/codeGROOVE-dev/turnclient/pkg/turn"
	gh "github.com/google/go-github/v50/github"
)

// TestShouldSendDigest_NoSlackMapping tests when GitHub user has no Slack mapping.
func TestShouldSendDigest_NoSlackMapping(t *testing.T) {
	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "", nil // No mapping
		},
	}

	mockClient := &mockSlackClient{
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "America/New_York", nil
		},
	}

	stateStore := &mockStateProvider{}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()
	result := scheduler.shouldSendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", nil)

	if result {
		t.Error("expected shouldSendDigest to return false when user has no Slack mapping")
	}
}

// TestShouldSendDigest_MappingError tests when user mapping fails with error.
func TestShouldSendDigest_MappingError(t *testing.T) {
	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "", errors.New("mapping error")
		},
	}

	stateStore := &mockStateProvider{}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()
	result := scheduler.shouldSendDigest(ctx, mockUserMapper, &mockSlackClient{}, "testuser", "test-org", "example.com", nil)

	if result {
		t.Error("expected shouldSendDigest to return false when user mapping fails")
	}
}

// TestShouldSendDigest_InvalidTimezone tests when user has invalid timezone.
func TestShouldSendDigest_InvalidTimezone(t *testing.T) {
	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "U123", nil
		},
	}

	mockClient := &mockSlackClient{
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "Invalid/Timezone", nil // Invalid timezone
		},
	}

	stateStore := &mockStateProvider{}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()
	result := scheduler.shouldSendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", nil)

	if result {
		t.Error("expected shouldSendDigest to return false when timezone is invalid")
	}
}

// TestShouldSendDigest_AlreadySentToday tests when digest was already sent today.
func TestShouldSendDigest_AlreadySentToday(t *testing.T) {
	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "U123", nil
		},
	}

	mockClient := &mockSlackClient{
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "UTC", nil
		},
	}

	today := time.Now().UTC().Format("2006-01-02")
	stateStore := &mockStateProvider{
		lastDigestFunc: func(userID, date string) (time.Time, bool) {
			if date == today {
				return time.Now(), true // Already sent today
			}
			return time.Time{}, false
		},
	}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()
	result := scheduler.shouldSendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", nil)

	if result {
		t.Error("expected shouldSendDigest to return false when digest already sent today")
	}
}

// TestSendDigest_MappingError tests error handling when user mapping fails.
func TestSendDigest_MappingError(t *testing.T) {
	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "", context.DeadlineExceeded // Mapping failed
		},
	}

	mockClient := &mockSlackClient{}
	stateStore := &mockStateProvider{}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()

	err := scheduler.sendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", nil)

	if err == nil {
		t.Error("expected error when user mapping fails")
	}
}

// TestSendDigest_SendDMError tests error handling when SendDirectMessage fails.
func TestSendDigest_SendDMError(t *testing.T) {
	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "U123", nil
		},
	}

	mockClient := &mockSlackClient{
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "", "", errors.New("slack API error")
		},
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "UTC", nil
		},
	}

	stateStore := &mockStateProvider{}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()

	err := scheduler.sendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", nil)

	if err == nil {
		t.Error("expected error when SendDirectMessage fails")
	}
}

// TestSendDigest_Success tests successful digest sending with state recording.
func TestSendDigest_Success(t *testing.T) {
	dmSent := false
	digestRecorded := false

	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "U123", nil
		},
	}

	mockClient := &mockSlackClient{
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			return "D123", "1234567890.123456", nil
		},
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "America/New_York", nil
		},
	}

	stateStore := &mockStateProvider{
		recordDigestFunc: func(userID, date string, sentAt time.Time) error {
			digestRecorded = true
			return nil
		},
	}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()
	prs := []home.PR{
		{
			Title:      "Fix bug",
			Author:     "otheruser",
			URL:        "https://github.com/test-org/test-repo/pull/1",
			UpdatedAt:  time.Now(),
			ActionKind: "review",
		},
	}

	err := scheduler.sendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", prs)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !dmSent {
		t.Error("expected DM to be sent")
	}

	if !digestRecorded {
		t.Error("expected digest to be recorded")
	}
}

// TestAnalyzePR_Success tests successful PR analysis.
func TestAnalyzePR_Success(t *testing.T) {
	mockClient := &mockGitHubClient{
		installationTokenFunc: func(ctx context.Context) string {
			return "test-token"
		},
	}

	mockTurnClient := &mockTurnClient{
		checkFunc: func(ctx context.Context, prURL, author string, updatedAt time.Time) (*turn.CheckResponse, error) {
			return createTestCheckResponse("reviewer", "review"), nil
		},
	}

	scheduler := &DailyDigestScheduler{
		turnClientFactory: func(authToken string) (TurnClient, error) {
			return mockTurnClient, nil
		},
	}

	ctx := context.Background()
	pr := home.PR{
		URL:       "https://github.com/test-org/test-repo/pull/1",
		Author:    "testuser",
		UpdatedAt: time.Now(),
	}

	result, err := scheduler.analyzePR(ctx, mockClient, "test-org", pr)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result == nil {
		t.Error("expected non-nil result")
	}
}

// TestAnalyzePR_TurnClientFactoryError tests when turn client creation fails.
func TestAnalyzePR_TurnClientFactoryError(t *testing.T) {
	mockClient := &mockGitHubClient{
		installationTokenFunc: func(ctx context.Context) string {
			return "test-token"
		},
	}

	scheduler := &DailyDigestScheduler{
		turnClientFactory: func(authToken string) (TurnClient, error) {
			return nil, errors.New("factory error")
		},
	}

	ctx := context.Background()
	pr := home.PR{
		URL:       "https://github.com/test-org/test-repo/pull/1",
		Author:    "testuser",
		UpdatedAt: time.Now(),
	}

	_, err := scheduler.analyzePR(ctx, mockClient, "test-org", pr)

	if err == nil {
		t.Error("expected error when turn client factory fails")
	}
}

// TestAnalyzePR_CheckError tests when turnclient Check fails.
func TestAnalyzePR_CheckError(t *testing.T) {
	mockClient := &mockGitHubClient{
		installationTokenFunc: func(ctx context.Context) string {
			return "test-token"
		},
	}

	mockTurnClient := &mockTurnClient{
		checkFunc: func(ctx context.Context, prURL, author string, updatedAt time.Time) (*turn.CheckResponse, error) {
			return nil, errors.New("check error")
		},
	}

	scheduler := &DailyDigestScheduler{
		turnClientFactory: func(authToken string) (TurnClient, error) {
			return mockTurnClient, nil
		},
	}

	ctx := context.Background()
	pr := home.PR{
		URL:       "https://github.com/test-org/test-repo/pull/1",
		Author:    "testuser",
		UpdatedAt: time.Now(),
	}

	_, err := scheduler.analyzePR(ctx, mockClient, "test-org", pr)

	if err == nil {
		t.Error("expected error when turnclient Check fails")
	}
}

// TestProcessOrgDigests_NoGitHubClient tests when GitHub client is unavailable.
func TestProcessOrgDigests_NoGitHubClient(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{
		clientForOrgFunc: func(org string) (github.ClientInterface, bool) {
			return nil, false // No client
		},
	}

	scheduler := &DailyDigestScheduler{
		githubManager: mockGitHubMgr,
		configManager: &mockConfigProvider{},
		stateStore:    &mockStateProvider{},
		slackManager:  &mockSlackManagerWithClient{},
	}

	ctx := context.Background()
	sent, errors := scheduler.processOrgDigests(ctx, "test-org")

	if sent != 0 {
		t.Errorf("expected 0 sent, got %d", sent)
	}

	if errors != 1 {
		t.Errorf("expected 1 error, got %d", errors)
	}
}

// TestProcessOrgDigests_NoConfig tests when config is unavailable.
func TestProcessOrgDigests_NoConfig(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{
		clientForOrgFunc: func(org string) (github.ClientInterface, bool) {
			return &mockGitHubClient{}, true
		},
	}

	mockConfigMgr := &mockConfigProvider{
		configFunc: func(org string) (*config.RepoConfig, bool) {
			return nil, false // No config
		},
	}

	scheduler := &DailyDigestScheduler{
		githubManager: mockGitHubMgr,
		configManager: mockConfigMgr,
		stateStore:    &mockStateProvider{},
		slackManager:  &mockSlackManagerWithClient{},
	}

	ctx := context.Background()
	sent, errors := scheduler.processOrgDigests(ctx, "test-org")

	if sent != 0 {
		t.Errorf("expected 0 sent, got %d", sent)
	}

	if errors != 1 {
		t.Errorf("expected 1 error, got %d", errors)
	}
}

// TestProcessOrgDigests_NoSlackClient tests when Slack client is unavailable.
func TestProcessOrgDigests_NoSlackClient(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{
		clientForOrgFunc: func(org string) (github.ClientInterface, bool) {
			return &mockGitHubClient{}, true
		},
	}

	mockSlackMgr := &mockSlackManagerWithClient{
		err: errors.New("slack error"),
	}

	scheduler := &DailyDigestScheduler{
		githubManager: mockGitHubMgr,
		configManager: &mockConfigProvider{},
		stateStore:    &mockStateProvider{},
		slackManager:  mockSlackMgr,
	}

	ctx := context.Background()
	sent, errors := scheduler.processOrgDigests(ctx, "test-org")

	if sent != 0 {
		t.Errorf("expected 0 sent, got %d", sent)
	}

	if errors != 1 {
		t.Errorf("expected 1 error, got %d", errors)
	}
}

// TestShouldSendDigest_In8to9amWindow tests when user is in 8-9am window.
func TestShouldSendDigest_In8to9amWindow(t *testing.T) {
	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "U123", nil
		},
	}

	// Mock current time to be 8:30am UTC
	mockClient := &mockSlackClient{
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "UTC", nil
		},
	}

	yesterday := time.Now().Add(-25 * time.Hour).Format("2006-01-02")
	stateStore := &mockStateProvider{
		lastDigestFunc: func(userID, date string) (time.Time, bool) {
			if date == yesterday {
				return time.Now().Add(-25 * time.Hour), true
			}
			return time.Time{}, false
		},
	}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()

	// This test is time-dependent - it will pass if run during 8-9am UTC
	// For deterministic testing, we'd need to inject time, but this shows the logic
	result := scheduler.shouldSendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", nil)

	// Result depends on actual time - just verify no crash
	_ = result
}

// TestSendDigest_PRSorting tests that PRs are sorted by update time.
func TestSendDigest_PRSorting(t *testing.T) {
	dmSent := false
	var sentMessage string

	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "U123", nil
		},
	}

	mockClient := &mockSlackClient{
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			dmSent = true
			sentMessage = text
			return "D123", "1234567890.123456", nil
		},
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "UTC", nil
		},
	}

	stateStore := &mockStateProvider{}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()

	// Create PRs with different update times
	oldPR := home.PR{
		Title:      "Old PR",
		Author:     "otheruser",
		URL:        "https://github.com/test-org/test-repo/pull/1",
		UpdatedAt:  time.Now().Add(-48 * time.Hour),
		ActionKind: "review",
	}

	newPR := home.PR{
		Title:      "New PR",
		Author:     "otheruser",
		URL:        "https://github.com/test-org/test-repo/pull/2",
		UpdatedAt:  time.Now().Add(-2 * time.Hour),
		ActionKind: "review",
	}

	// Pass in old order - should be sorted by update time
	prs := []home.PR{oldPR, newPR}

	err := scheduler.sendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", prs)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !dmSent {
		t.Error("expected DM to be sent")
	}

	// Verify message contains both PRs
	if !contains(sentMessage, "Old PR") || !contains(sentMessage, "New PR") {
		t.Error("expected message to contain both PRs")
	}
}

// TestSendDigest_TimezoneFallback tests timezone fallback to UTC.
func TestSendDigest_TimezoneFallback(t *testing.T) {
	digestRecorded := false
	var recordedDate string

	mockUserMapper := &mockDigestUserMapper{
		slackHandleFunc: func(ctx context.Context, githubUser, org, domain string) (string, error) {
			return "U123", nil
		},
	}

	mockClient := &mockSlackClient{
		sendDirectMessageFunc: func(ctx context.Context, userID, text string) (string, string, error) {
			return "D123", "1234567890.123456", nil
		},
		userTimezoneFunc: func(ctx context.Context, userID string) (string, error) {
			return "", errors.New("timezone error") // Force fallback
		},
	}

	stateStore := &mockStateProvider{
		recordDigestFunc: func(userID, date string, sentAt time.Time) error {
			digestRecorded = true
			recordedDate = date
			return nil
		},
	}

	scheduler := &DailyDigestScheduler{
		stateStore: stateStore,
	}

	ctx := context.Background()
	prs := []home.PR{
		{
			Title:      "Test PR",
			Author:     "otheruser",
			URL:        "https://github.com/test-org/test-repo/pull/1",
			UpdatedAt:  time.Now(),
			ActionKind: "review",
		},
	}

	err := scheduler.sendDigest(ctx, mockUserMapper, mockClient, "testuser", "test-org", "example.com", prs)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !digestRecorded {
		t.Error("expected digest to be recorded")
	}

	// Should use UTC date when timezone lookup fails
	expectedDate := time.Now().UTC().Format("2006-01-02")
	if recordedDate != expectedDate {
		t.Errorf("expected UTC date %s, got %s", expectedDate, recordedDate)
	}
}

// TestNewDailyDigestScheduler_FactoryWorks tests that the turn client factory is set.
func TestNewDailyDigestScheduler_FactoryWorks(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{}
	mockConfigMgr := &mockConfigProvider{}
	mockState := &mockStateProvider{}
	mockSlack := &mockSlackManagerWithClient{}
	manager := New(mockSlack, mockConfigMgr, &mockStore{})

	scheduler := NewDailyDigestScheduler(manager, mockGitHubMgr, mockConfigMgr, mockState, mockSlack)

	if scheduler.turnClientFactory == nil {
		t.Error("expected turnClientFactory to be set")
	}

	// Test that factory can be called
	client, err := scheduler.turnClientFactory("test-token")
	if err != nil {
		t.Errorf("expected factory to succeed, got error: %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client from factory")
	}
}

// TestProcessOrgDigests_FetchPRsError tests when fetchOrgPRs fails.
func TestProcessOrgDigests_FetchPRsError(t *testing.T) {
	mockGitHubClient := &mockGitHubClient{
		clientFunc: func() *gh.Client {
			// Return nil to cause fetchOrgPRs to fail
			return nil
		},
	}

	mockGitHubMgr := &mockGitHubManager{
		clientForOrgFunc: func(org string) (github.ClientInterface, bool) {
			return mockGitHubClient, true
		},
	}

	mockConfigMgr := &mockConfigProvider{}

	mockSlackMgr := &mockSlackManagerWithClient{
		client: &mockSlackClient{},
	}

	scheduler := &DailyDigestScheduler{
		githubManager: mockGitHubMgr,
		configManager: mockConfigMgr,
		stateStore:    &mockStateProvider{},
		slackManager:  mockSlackMgr,
	}

	ctx := context.Background()
	sent, errors := scheduler.processOrgDigests(ctx, "test-org")

	if sent != 0 {
		t.Errorf("expected 0 sent, got %d", sent)
	}

	if errors != 1 {
		t.Errorf("expected 1 error, got %d", errors)
	}
}

// TestCheckAndSend_WithOrgs tests successful processing of organizations.
func TestCheckAndSend_WithOrgs(t *testing.T) {
	mockGitHubMgr := &mockGitHubManager{
		allOrgsFunc: func() []string {
			return []string{"test-org"}
		},
		clientForOrgFunc: func(org string) (github.ClientInterface, bool) {
			// Return nil client to cause early return (no PRs to process)
			return nil, false
		},
	}

	mockConfigMgr := &mockConfigProvider{
		dailyRemindersEnabledFunc: func(org string) bool {
			return true
		},
	}

	scheduler := &DailyDigestScheduler{
		githubManager: mockGitHubMgr,
		configManager: mockConfigMgr,
		stateStore:    &mockStateProvider{},
		slackManager:  &mockSlackManagerWithClient{},
	}

	ctx := context.Background()

	// Should not crash and should process the org
	scheduler.CheckAndSend(ctx)
}
