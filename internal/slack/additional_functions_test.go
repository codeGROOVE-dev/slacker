package slack

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/slacktest"
	slackapi "github.com/slack-go/slack"
)

// TestPostThreadReply tests posting a reply to a thread.
func TestPostThreadReply(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddChannel("C123", "general", true)
	mockSlack.AddChannelMember("C123", "U123BOT")

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	// Post thread reply
	err := client.PostThreadReply(ctx, "C123", "1234567890.123456", "Reply text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify reply was posted
	messages := mockSlack.GetPostedMessages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	if messages[0].Channel != "C123" {
		t.Errorf("expected channel C123, got %s", messages[0].Channel)
	}
}

// TestPostThreadReply_CircuitBreakerOpen tests PostThreadReply with circuit breaker open.
func TestPostThreadReply_CircuitBreakerOpen(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "open",
			failureLimit: 10,
			openUntil:    time.Now().Add(5 * time.Minute),
		},
	}

	ctx := context.Background()

	err := client.PostThreadReply(ctx, "C123", "1234567890.123456", "Reply text")
	if err == nil {
		t.Fatal("expected error when circuit breaker is open, got nil")
	}

	if err.Error() != "slack API circuit breaker open - service temporarily unavailable" {
		t.Errorf("expected circuit breaker error, got: %v", err)
	}
}

// TestAddReaction tests adding a reaction to a message.
func TestAddReaction(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	err := client.AddReaction(ctx, "C123", "1234567890.123456", "thumbsup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestAddReaction_CircuitBreakerOpen tests AddReaction with circuit breaker open.
func TestAddReaction_CircuitBreakerOpen(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "open",
			failureLimit: 10,
			openUntil:    time.Now().Add(5 * time.Minute),
		},
	}

	ctx := context.Background()

	err := client.AddReaction(ctx, "C123", "1234567890.123456", "thumbsup")
	if err == nil {
		t.Fatal("expected error when circuit breaker is open, got nil")
	}
}

// TestRemoveReaction tests removing a reaction from a message.
func TestRemoveReaction(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	err := client.RemoveReaction(ctx, "C123", "1234567890.123456", "thumbsup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRemoveReaction_CircuitBreakerOpen tests RemoveReaction with circuit breaker open.
func TestRemoveReaction_CircuitBreakerOpen(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "open",
			failureLimit: 10,
			openUntil:    time.Now().Add(5 * time.Minute),
		},
	}

	ctx := context.Background()

	err := client.RemoveReaction(ctx, "C123", "1234567890.123456", "thumbsup")
	if err == nil {
		t.Fatal("expected error when circuit breaker is open, got nil")
	}
}

// TestUpdateReactions tests updating reactions without previous reactions.
func TestUpdateReactions(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	// Update with one new reaction
	err := client.UpdateReactions(ctx, "C123", "1234567890.123456", "awaiting_review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestUpdateReactionsWithPrevious tests updating reactions with previous reactions.
func TestUpdateReactionsWithPrevious(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	// Update with both previous and new reactions
	err := client.UpdateReactionsWithPrevious(ctx, "C123", "1234567890.123456", "tests_running", "awaiting_review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHasRecentDMAboutPR_NoRecent tests when no recent DM exists.
func TestHasRecentDMAboutPR_NoRecent(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:       slackClient,
		teamID:    "T123",
		cache:     &apiCache{entries: make(map[string]cacheEntry)},
		stateStore: nil, // No state store = no recent DMs
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	hasRecent, err := client.HasRecentDMAboutPR(ctx, "U001", "https://github.com/test/repo/pull/123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasRecent {
		t.Error("expected no recent DM when state store is nil")
	}
}

// TestSaveDMMessageInfo tests saving DM message information.
func TestSaveDMMessageInfo(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:       slackClient,
		teamID:    "T123",
		cache:     &apiCache{entries: make(map[string]cacheEntry)},
		stateStore: nil, // No state store - should handle gracefully
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	// Should not panic when state store is nil
	err := client.SaveDMMessageInfo(ctx, "U001", "https://github.com/test/repo/pull/123", "D123", "1234567890.123456", "Test message")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestUpdateDMMessage tests updating a DM message.
func TestUpdateDMMessage(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	ctx := context.Background()

	// Update DM message
	err := client.UpdateDMMessage(ctx, "D123", "1234567890.123456", "Updated DM text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify update was recorded
	updates := mockSlack.GetUpdatedMessages()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	if updates[0].Channel != "D123" {
		t.Errorf("expected DM channel D123, got %s", updates[0].Channel)
	}

	if updates[0].Text != "Updated DM text" {
		t.Errorf("expected updated text, got %q", updates[0].Text)
	}
}

// TestAPI tests getting raw Slack API client.
func TestAPI(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "closed",
			failureLimit: 10,
		},
	}

	api := client.API()
	if api == nil {
		t.Error("expected non-nil API client")
	}
}
