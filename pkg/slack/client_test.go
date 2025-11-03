package slack

import (
	"context"
	"strings"
	"testing"

	"github.com/codeGROOVE-dev/slacker/pkg/slacktest"
	slackapi "github.com/slack-go/slack"
)

// TestPostThread verifies that PostThread sends messages to the correct channel with correct content.
func TestPostThread(t *testing.T) {
	t.Parallel()

	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddChannel("C123", "general", true)
	mockSlack.AddChannelMember("C123", "U123BOT") // Add bot to channel

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
	}

	ctx := context.Background()

	tests := []struct {
		name           string
		channelID      string
		text           string
		attachments    []slackapi.Attachment
		expectError    bool
		validateResult func(t *testing.T, messages []*slacktest.PostedMessage)
	}{
		{
			name:        "simple message",
			channelID:   "C123",
			text:        "Hello, world!",
			attachments: nil,
			expectError: false,
			validateResult: func(t *testing.T, messages []*slacktest.PostedMessage) {
				if len(messages) != 1 {
					t.Fatalf("expected 1 message, got %d", len(messages))
				}
				if messages[0].Channel != "C123" {
					t.Errorf("expected channel C123, got %s", messages[0].Channel)
				}
				if !strings.Contains(messages[0].Text, "Hello, world!") {
					t.Errorf("expected text to contain 'Hello, world!', got %q", messages[0].Text)
				}
			},
		},
		{
			name:        "message with emoji",
			channelID:   "C123",
			text:        ":rocket: Deploy successful!",
			attachments: nil,
			expectError: false,
			validateResult: func(t *testing.T, messages []*slacktest.PostedMessage) {
				if len(messages) != 1 {
					t.Fatalf("expected 1 message, got %d", len(messages))
				}
				if !strings.Contains(messages[0].Text, ":rocket:") {
					t.Errorf("expected emoji in text, got %q", messages[0].Text)
				}
			},
		},
		{
			name:        "message with markdown link",
			channelID:   "C123",
			text:        "Check out <https://github.com/test/repo/pull/123|PR #123>",
			attachments: nil,
			expectError: false,
			validateResult: func(t *testing.T, messages []*slacktest.PostedMessage) {
				if len(messages) != 1 {
					t.Fatalf("expected 1 message, got %d", len(messages))
				}
				if !strings.Contains(messages[0].Text, "https://github.com") {
					t.Errorf("expected URL in text, got %q", messages[0].Text)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSlack.Reset()

			messageTS, err := client.PostThread(ctx, tt.channelID, tt.text, tt.attachments)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if messageTS == "" {
				t.Error("expected non-empty message timestamp")
			}

			messages := mockSlack.GetPostedMessages()
			tt.validateResult(t, messages)
		})
	}
}

// TestUpdateMessage verifies that UpdateMessage modifies existing messages correctly.
func TestUpdateMessage(t *testing.T) {
	t.Parallel()

	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddChannel("C123", "general", true)

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
	}

	ctx := context.Background()

	tests := []struct {
		name        string
		channelID   string
		timestamp   string
		newText     string
		expectError bool
	}{
		{
			name:        "update with new emoji",
			channelID:   "C123",
			timestamp:   "1234567890.123456",
			newText:     ":white_check_mark: Updated message",
			expectError: false,
		},
		{
			name:        "update with different content",
			channelID:   "C123",
			timestamp:   "1234567890.123456",
			newText:     ":hourglass: Waiting for review",
			expectError: false,
		},
		{
			name:        "update with longer text",
			channelID:   "C123",
			timestamp:   "1234567890.123456",
			newText:     ":rocket: Ready to merge · All checks passing · 3 approvals",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSlack.Reset()

			err := client.UpdateMessage(ctx, tt.channelID, tt.timestamp, tt.newText)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify update was recorded
			updates := mockSlack.GetUpdatedMessages()
			if len(updates) != 1 {
				t.Fatalf("expected 1 update, got %d", len(updates))
			}

			if updates[0].Channel != tt.channelID {
				t.Errorf("expected channel %s, got %s", tt.channelID, updates[0].Channel)
			}

			if updates[0].Timestamp != tt.timestamp {
				t.Errorf("expected timestamp %s, got %s", tt.timestamp, updates[0].Timestamp)
			}

			if updates[0].Text != tt.newText {
				t.Errorf("expected text %q, got %q", tt.newText, updates[0].Text)
			}
		})
	}
}

// TestSendDirectMessage verifies that DMs are sent to the correct users.
func TestSendDirectMessage(t *testing.T) {
	t.Parallel()

	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddUser("alice@example.com", "U001", "alice")
	mockSlack.AddUser("bob@example.com", "U002", "bob")

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
	}

	ctx := context.Background()

	tests := []struct {
		name        string
		userID      string
		text        string
		expectError bool
	}{
		{
			name:        "DM to alice",
			userID:      "U001",
			text:        ":hourglass: PR needs review <https://github.com/test/repo/pull/123|repo#123>",
			expectError: false,
		},
		{
			name:        "DM to bob",
			userID:      "U002",
			text:        ":rocket: Your PR is ready to merge",
			expectError: false,
		},
		{
			name:        "DM with multiple lines",
			userID:      "U001",
			text:        ":white_check_mark: Approved!\n\nYou can merge now.",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSlack.Reset()

			dmChannelID, messageTS, err := client.SendDirectMessage(ctx, tt.userID, tt.text)

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if dmChannelID == "" {
				t.Error("expected non-empty DM channel ID")
			}

			if messageTS == "" {
				t.Error("expected non-empty message timestamp")
			}

			// Verify DM was sent
			messages := mockSlack.GetPostedMessages()
			if len(messages) != 1 {
				t.Fatalf("expected 1 DM, got %d", len(messages))
			}

			// DM channel IDs start with 'D'
			if !strings.HasPrefix(messages[0].Channel, "D") {
				t.Errorf("expected DM channel (starts with D), got %s", messages[0].Channel)
			}

			if !strings.Contains(messages[0].Text, tt.text) {
				t.Errorf("expected text to contain %q, got %q", tt.text, messages[0].Text)
			}
		})
	}
}

// TestMessageMutationSequence verifies that we can post, then update the same message.
func TestMessageMutationSequence(t *testing.T) {
	t.Parallel()

	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddChannel("C123", "general", true)
	mockSlack.AddChannelMember("C123", "U123BOT") // Add bot to channel

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
	}

	ctx := context.Background()

	// Step 1: Post initial message
	initialText := ":test_tube: Tests running"
	messageTS, err := client.PostThread(ctx, "C123", initialText, nil)
	if err != nil {
		t.Fatalf("failed to post initial message: %v", err)
	}

	if messageTS == "" {
		t.Fatal("expected non-empty message timestamp")
	}

	// Verify initial post
	messages := mockSlack.GetPostedMessages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 posted message, got %d", len(messages))
	}
	if !strings.Contains(messages[0].Text, ":test_tube:") {
		t.Errorf("expected initial emoji in message, got %q", messages[0].Text)
	}

	// Step 2: Update to show tests passing
	updatedText := ":white_check_mark: Tests passed"
	err = client.UpdateMessage(ctx, "C123", messageTS, updatedText)
	if err != nil {
		t.Fatalf("failed to update message: %v", err)
	}

	// Verify update
	updates := mockSlack.GetUpdatedMessages()
	if len(updates) != 1 {
		t.Fatalf("expected 1 updated message, got %d", len(updates))
	}
	if updates[0].Timestamp != messageTS {
		t.Errorf("expected update to same timestamp %s, got %s", messageTS, updates[0].Timestamp)
	}
	if updates[0].Text != updatedText {
		t.Errorf("expected updated text %q, got %q", updatedText, updates[0].Text)
	}

	// Step 3: Update again to show merge
	finalText := ":rocket: Merged"
	err = client.UpdateMessage(ctx, "C123", messageTS, finalText)
	if err != nil {
		t.Fatalf("failed to update message second time: %v", err)
	}

	// Verify second update
	updates = mockSlack.GetUpdatedMessages()
	if len(updates) != 2 {
		t.Fatalf("expected 2 total updates, got %d", len(updates))
	}
	if updates[1].Text != finalText {
		t.Errorf("expected final text %q, got %q", finalText, updates[1].Text)
	}
}

// TestDMMutationSequence verifies that we can send a DM, then update it.
func TestDMMutationSequence(t *testing.T) {
	t.Parallel()

	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddUser("alice@example.com", "U001", "alice")

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
	}

	ctx := context.Background()

	// Step 1: Send initial DM
	initialText := ":hourglass: Your review is needed on PR #123"
	dmChannelID, messageTS, err := client.SendDirectMessage(ctx, "U001", initialText)
	if err != nil {
		t.Fatalf("failed to send initial DM: %v", err)
	}

	if dmChannelID == "" || messageTS == "" {
		t.Fatal("expected non-empty DM channel ID and message timestamp")
	}

	// Verify initial DM
	messages := mockSlack.GetPostedMessages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 DM sent, got %d", len(messages))
	}

	// Step 2: Update the DM with new status
	updatedText := ":white_check_mark: PR #123 has been approved!"
	err = client.UpdateMessage(ctx, dmChannelID, messageTS, updatedText)
	if err != nil {
		t.Fatalf("failed to update DM: %v", err)
	}

	// Verify update
	updates := mockSlack.GetUpdatedMessages()
	if len(updates) != 1 {
		t.Fatalf("expected 1 DM update, got %d", len(updates))
	}
	if updates[0].Channel != dmChannelID {
		t.Errorf("expected update to DM channel %s, got %s", dmChannelID, updates[0].Channel)
	}
	if updates[0].Text != updatedText {
		t.Errorf("expected updated text %q, got %q", updatedText, updates[0].Text)
	}
}

// TestMultipleChannelPosts verifies posting to multiple channels works correctly.
func TestMultipleChannelPosts(t *testing.T) {
	t.Parallel()

	mockSlack := slacktest.New()
	defer mockSlack.Close()

	mockSlack.AddChannel("C111", "dev", true)
	mockSlack.AddChannelMember("C111", "U123BOT") // Add bot to channel
	mockSlack.AddChannel("C222", "qa", true)
	mockSlack.AddChannelMember("C222", "U123BOT") // Add bot to channel
	mockSlack.AddChannel("C333", "prod", true)
	mockSlack.AddChannelMember("C333", "U123BOT") // Add bot to channel

	slackClient := slackapi.New("test-token", slackapi.OptionAPIURL(mockSlack.URL+"/api/"))
	client := &Client{
		api:    slackClient,
		teamID: "T123",
		cache:  &apiCache{entries: make(map[string]cacheEntry)},
	}

	ctx := context.Background()

	// Post same PR to multiple channels
	channels := []struct {
		id   string
		name string
	}{
		{"C111", "dev"},
		{"C222", "qa"},
		{"C333", "prod"},
	}

	prText := ":new: New PR ready for review <https://github.com/test/repo/pull/456|repo#456>"

	for _, ch := range channels {
		_, err := client.PostThread(ctx, ch.id, prText, nil)
		if err != nil {
			t.Fatalf("failed to post to channel %s: %v", ch.name, err)
		}
	}

	// Verify all posts
	messages := mockSlack.GetPostedMessages()
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages (one per channel), got %d", len(messages))
	}

	// Verify each channel got the message
	channelsSeen := make(map[string]bool)
	for _, msg := range messages {
		channelsSeen[msg.Channel] = true
		if !strings.Contains(msg.Text, prText) {
			t.Errorf("expected text %q in channel %s, got %q", prText, msg.Channel, msg.Text)
		}
	}

	if len(channelsSeen) != 3 {
		t.Errorf("expected messages in 3 different channels, saw %d", len(channelsSeen))
	}
}
