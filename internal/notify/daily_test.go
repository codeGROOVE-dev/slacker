package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/home"
)

func TestFormatDigestMessage(t *testing.T) {
	tests := []struct {
		name     string
		time     time.Time
		incoming []home.PR
		outgoing []home.PR
		expected string
	}{
		{
			name: "incoming PRs only at 8:30am",
			time: time.Date(2025, 10, 22, 8, 30, 0, 0, time.UTC),
			incoming: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/slacker/pull/123",
					Title:      "Add daily digest feature",
					Author:     "otheruser",
					ActionKind: "review",
				},
			},
			outgoing: nil,
			expected: `☀️ *Good morning!*

*To Review:*
:hourglass: <https://github.com/codeGROOVE-dev/slacker/pull/123|Add daily digest feature> · review

_Your daily digest from Ready to Review_`,
		},
		{
			name:     "outgoing PRs only at 8:15am",
			time:     time.Date(2025, 10, 22, 8, 15, 0, 0, time.UTC),
			incoming: nil,
			outgoing: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/slacker/pull/124",
					Title:      "Fix authentication bug",
					Author:     "testuser",
					ActionKind: "address-feedback",
				},
			},
			expected: `🌻 *Hello sunshine!*

*Your PRs:*
:hourglass: <https://github.com/codeGROOVE-dev/slacker/pull/124|Fix authentication bug> · address-feedback

_Your daily digest from Ready to Review_`,
		},
		{
			name: "both incoming and outgoing at 8:45am",
			time: time.Date(2025, 10, 22, 8, 45, 0, 0, time.UTC),
			incoming: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/goose/pull/456",
					Title:      "Implement new API endpoint",
					Author:     "colleague",
					ActionKind: "review",
				},
				{
					URL:        "https://github.com/codeGROOVE-dev/goose/pull/457",
					Title:      "Refactor database layer",
					Author:     "teammate",
					ActionKind: "approve",
				},
			},
			outgoing: []home.PR{
				{
					URL:        "https://github.com/codeGROOVE-dev/goose/pull/458",
					Title:      "Update documentation",
					Author:     "testuser",
					ActionKind: "merge",
				},
			},
			expected: `🌻 *Hello sunshine!*

*To Review:*
:hourglass: <https://github.com/codeGROOVE-dev/goose/pull/456|Implement new API endpoint> · review
:hourglass: <https://github.com/codeGROOVE-dev/goose/pull/457|Refactor database layer> · approve

*Your PRs:*
:hourglass: <https://github.com/codeGROOVE-dev/goose/pull/458|Update documentation> · merge

_Your daily digest from Ready to Review_`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler := &DailyDigestScheduler{}
			got := scheduler.formatDigestMessageAt(tt.incoming, tt.outgoing, tt.time)

			if got != tt.expected {
				t.Errorf("formatDigestMessageAt() mismatch\nGot:\n%s\n\nExpected:\n%s", got, tt.expected)
			}
		})
	}
}

func TestDigestMessageVariety(t *testing.T) {
	// Test that different times produce different greetings
	scheduler := &DailyDigestScheduler{}

	incoming := []home.PR{
		{
			URL:        "https://github.com/codeGROOVE-dev/slacker/pull/1",
			Title:      "Test PR",
			Author:     "other",
			ActionKind: "review",
		},
	}

	// Collect messages at different times
	messages := make(map[string]bool)
	for hour := 8; hour < 9; hour++ {
		for minute := 0; minute < 60; minute += 15 {
			testTime := time.Date(2025, 10, 22, hour, minute, 0, 0, time.UTC)
			msg := scheduler.formatDigestMessageAt(incoming, nil, testTime)
			messages[msg] = true
		}
	}

	// Should have at least 2 different message variations in the 8-9am window
	if len(messages) < 2 {
		t.Errorf("Expected message variety, but got only %d unique messages in 1-hour window", len(messages))
	}

	t.Logf("Generated %d unique message variations across the 8-9am window", len(messages))
}

// TestDailyDigestExample shows what an actual daily digest looks like with both sections.
func TestDailyDigestExample(t *testing.T) {
	scheduler := &DailyDigestScheduler{}

	// Example: User has 2 incoming PRs to review and 1 outgoing PR needing attention at 8:30am
	exampleTime := time.Date(2025, 10, 22, 8, 30, 0, 0, time.UTC)

	exampleIncoming := []home.PR{
		{
			URL:        "https://github.com/codeGROOVE-dev/goose/pull/127",
			Title:      "Add support for custom prompts",
			Author:     "colleague",
			ActionKind: "review",
		},
		{
			URL:        "https://github.com/codeGROOVE-dev/sprinkler/pull/15",
			Title:      "Implement WebSocket reconnection logic",
			Author:     "teammate",
			ActionKind: "approve",
		},
	}

	exampleOutgoing := []home.PR{
		{
			URL:        "https://github.com/codeGROOVE-dev/slacker/pull/48",
			Title:      "Update DM messages when PR is merged",
			Author:     "testuser",
			ActionKind: "address-feedback",
		},
	}

	message := scheduler.formatDigestMessageAt(exampleIncoming, exampleOutgoing, exampleTime)

	// Log the example for documentation purposes
	t.Logf("Example daily digest DM:\n\n%s\n", message)

	// Verify it has the expected structure
	if len(message) == 0 {
		t.Error("Message should not be empty")
	}

	// Should contain both section headers
	if !strings.Contains(message, "*To Review:*") {
		t.Error("Message should contain 'To Review:' header")
	}
	if !strings.Contains(message, "*Your PRs:*") {
		t.Error("Message should contain 'Your PRs:' header")
	}

	// Should contain all PR URLs
	allPRs := append(exampleIncoming, exampleOutgoing...)
	for _, pr := range allPRs {
		if !strings.Contains(message, pr.URL) {
			t.Errorf("Message should contain PR URL: %s", pr.URL)
		}
	}

	// Should contain footer
	if !strings.Contains(message, "Your daily digest from Ready to Review") {
		t.Error("Message should contain footer")
	}
}
