package slack

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/slacktest"
	slackapi "github.com/slack-go/slack"
)

// TestPostThread_CircuitBreakerOpen tests that PostThread returns an error when circuit breaker is open.
func TestPostThread_CircuitBreakerOpen(t *testing.T) {
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
			state:        "open",
			failureLimit: 10,
			openUntil:    time.Now().Add(5 * time.Minute), // Set openUntil to future
		},
	}

	ctx := context.Background()

	_, err := client.PostThread(ctx, "C123", "Test message", nil)
	if err == nil {
		t.Fatal("expected error when circuit breaker is open, got nil")
	}

	if err.Error() != "slack API circuit breaker open - service temporarily unavailable" {
		t.Errorf("expected circuit breaker error, got: %v", err)
	}
}

// TestPostThread_BotNotInChannel tests error when bot is not in channel.
func TestPostThread_BotNotInChannel(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	// Create channel but DON'T add bot as member
	mockSlack.AddChannel("C123", "general", true)

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

	_, err := client.PostThread(ctx, "C123", "Test message", nil)
	if err == nil {
		t.Fatal("expected error when bot not in channel, got nil")
	}

	// Should get "bot is not a member" error
	if err.Error() != "bot is not a member of channel C123 - please invite the bot to the channel first" {
		t.Errorf("expected bot not member error, got: %v", err)
	}
}

// TestPostThread_LongText tests posting message with text longer than 100 characters.
func TestPostThread_LongText(t *testing.T) {
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

	// Create text longer than 100 chars (triggers preview truncation in logging)
	longText := "This is a very long message that exceeds one hundred characters to test the text preview truncation logic in the logging code"
	messageTS, err := client.PostThread(ctx, "C123", longText, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if messageTS == "" {
		t.Error("expected non-empty message timestamp")
	}

	messages := mockSlack.GetPostedMessages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	// Full text should be posted (not truncated)
	if messages[0].Text != longText {
		t.Errorf("expected full text to be posted, got truncated: %q", messages[0].Text)
	}
}

// TestUpdateMessage_CircuitBreakerOpen tests that UpdateMessage returns an error when circuit breaker is open.
func TestUpdateMessage_CircuitBreakerOpen(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddChannel("C123", "general", true)

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "open",
			failureLimit: 10,
			openUntil:    time.Now().Add(5 * time.Minute), // Set openUntil to future
		},
	}

	ctx := context.Background()

	err := client.UpdateMessage(ctx, "C123", "1234567890.123456", "Updated text")
	if err == nil {
		t.Fatal("expected error when circuit breaker is open, got nil")
	}

	if err.Error() != "slack API circuit breaker open - service temporarily unavailable" {
		t.Errorf("expected circuit breaker error, got: %v", err)
	}
}

// TestSendDirectMessage_CircuitBreakerOpen tests that SendDirectMessage returns an error when circuit breaker is open.
func TestSendDirectMessage_CircuitBreakerOpen(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddUser("alice@example.com", "U001", "alice")

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
		breaker: &circuitBreaker{
			state:        "open",
			failureLimit: 10,
			openUntil:    time.Now().Add(5 * time.Minute), // Set openUntil to future
		},
	}

	ctx := context.Background()

	_, _, err := client.SendDirectMessage(ctx, "U001", "Test DM")
	if err == nil {
		t.Fatal("expected error when circuit breaker is open, got nil")
	}

	if err.Error() != "slack API circuit breaker open - service temporarily unavailable" {
		t.Errorf("expected circuit breaker error, got: %v", err)
	}
}

// TestSendDirectMessage_LongText tests sending DM with long text.
func TestSendDirectMessage_LongText(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddUser("alice@example.com", "U001", "alice")

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

	// Long text (>100 chars) triggers preview truncation in logs
	longText := "This is a very long direct message that exceeds one hundred characters to test the text preview truncation logic in the logging code"

	dmChannelID, messageTS, err := client.SendDirectMessage(ctx, "U001", longText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dmChannelID == "" || messageTS == "" {
		t.Fatal("expected non-empty DM channel ID and message timestamp")
	}
}
