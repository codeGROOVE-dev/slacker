package slacktest

import (
	"errors"
	"testing"

	"github.com/slack-go/slack"
)

func TestMockServerUserLookup(t *testing.T) {
	// Create mock server
	server := New()
	defer server.Close()

	// Add test user
	server.AddUser("test@example.com", "U001", "testuser")

	// Create Slack client pointing to mock
	client := slack.New("test-token", slack.OptionAPIURL(server.URL+"/api/"))

	// Lookup user by email
	user, err := client.GetUserByEmail("test@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}

	if user.ID != "U001" {
		t.Errorf("Expected user ID U001, got %s", user.ID)
	}

	if user.Name != "testuser" {
		t.Errorf("Expected username testuser, got %s", user.Name)
	}

	// Verify email lookup was tracked
	lookups := server.GetEmailLookups()
	if len(lookups) != 1 {
		t.Errorf("Expected 1 email lookup, got %d", len(lookups))
	}

	if lookups[0] != "test@example.com" {
		t.Errorf("Expected lookup for test@example.com, got %s", lookups[0])
	}
}

func TestMockServerUserNotFound(t *testing.T) {
	server := New()
	defer server.Close()

	client := slack.New("test-token", slack.OptionAPIURL(server.URL+"/api/"))

	// Try to lookup non-existent user
	_, err := client.GetUserByEmail("notfound@example.com")
	if err == nil {
		t.Error("Expected error for non-existent user, got nil")
	}

	// Should be a slack error
	var slackErr slack.SlackErrorResponse
	if errors.As(err, &slackErr) {
		if slackErr.Err != "users_not_found" {
			t.Errorf("Expected 'users_not_found' error, got '%s'", slackErr.Err)
		}
	} else {
		t.Errorf("Expected slack.SlackErrorResponse, got %T: %v", err, err)
	}
}
