package slack

import (
	"context"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/slacktest"
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

// TestAddReaction tests adding a reaction to a message.

// TestHasRecentDMAboutPR_NoRecent tests when no recent DM exists.
func TestHasRecentDMAboutPR_NoRecent(t *testing.T) {
	mockSlack := slacktest.New()
	defer mockSlack.Close()

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:        slackClient,
		teamID:     "T123",
		cache:      &apiCache{entries: make(map[string]cacheEntry)},
		stateStore: nil, // No state store = no recent DMs
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
		api:        slackClient,
		teamID:     "T123",
		cache:      &apiCache{entries: make(map[string]cacheEntry)},
		stateStore: nil, // No state store - should handle gracefully
	}

	ctx := context.Background()

	// Should not panic when state store is nil
	err := client.SaveDMMessageInfo(ctx, "U001", "https://github.com/test/repo/pull/123", "D123", "1234567890.123456", "Test message")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
