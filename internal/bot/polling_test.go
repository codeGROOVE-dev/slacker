package bot

import (
	"testing"
	"time"
)

// TestClosedPRPollingWindow verifies that the closed PR polling window is sufficient
// to catch PRs closed during sprinkler outages.
//
// This test documents a known bug: If sprinkler is down for more than 1 hour when
// a PR is closed, polling will permanently miss the closed PR update because
// ListClosedPRs only looks back 1 hour.
//
// See: internal/bot/polling.go:98 - ListClosedPRs(ctx, org, 1)
func TestClosedPRPollingWindow(t *testing.T) {
	tests := []struct {
		name             string
		prClosedAt       time.Time
		pollingRunsAt    time.Time
		lookbackHours    int
		expectPRIncluded bool
		scenario         string
	}{
		{
			name:             "PR closed 5 minutes ago - should be caught",
			prClosedAt:       time.Now().Add(-5 * time.Minute),
			pollingRunsAt:    time.Now(),
			lookbackHours:    1,
			expectPRIncluded: true,
			scenario:         "Normal operation: polling catches recent closure",
		},
		{
			name:             "PR closed 59 minutes ago - should be caught",
			prClosedAt:       time.Now().Add(-59 * time.Minute),
			pollingRunsAt:    time.Now(),
			lookbackHours:    1,
			expectPRIncluded: true,
			scenario:         "Edge case: just within 1-hour window",
		},
		{
			name:             "PR closed 61 minutes ago - MISSED (BUG)",
			prClosedAt:       time.Now().Add(-61 * time.Minute),
			pollingRunsAt:    time.Now(),
			lookbackHours:    1,
			expectPRIncluded: false, // BUG: This PR will never be updated
			scenario:         "BUG: Sprinkler down 1h+ when PR closed - update permanently missed",
		},
		{
			name:             "PR closed 2 hours ago - MISSED (BUG)",
			prClosedAt:       time.Now().Add(-2 * time.Hour),
			pollingRunsAt:    time.Now(),
			lookbackHours:    1,
			expectPRIncluded: false, // BUG: This PR will never be updated
			scenario:         "BUG: Extended sprinkler outage - update permanently missed",
		},
		{
			name:             "PR closed 23 hours ago - would be caught with 24h window",
			prClosedAt:       time.Now().Add(-23 * time.Hour),
			pollingRunsAt:    time.Now(),
			lookbackHours:    24,
			expectPRIncluded: true,
			scenario:         "With 24h window: catches PRs from extended outages",
		},
		{
			name:             "PR closed 25 hours ago - missed even with 24h window",
			prClosedAt:       time.Now().Add(-25 * time.Hour),
			pollingRunsAt:    time.Now(),
			lookbackHours:    24,
			expectPRIncluded: false,
			scenario:         "Even 24h window has limits - very extended outage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate the lookback window start time
			windowStart := tt.pollingRunsAt.Add(-time.Duration(tt.lookbackHours) * time.Hour)

			// Simulate what ListClosedPRs does: filter by updated >= windowStart
			// This mimics the logic in internal/github/graphql.go:137-143
			prIncluded := !tt.prClosedAt.Before(windowStart)

			if prIncluded != tt.expectPRIncluded {
				t.Errorf("SCENARIO: %s\n"+
					"PR closed at: %s\n"+
					"Polling at:   %s\n"+
					"Lookback:     %dh (window start: %s)\n"+
					"Time since close: %v\n"+
					"Expected included: %v\n"+
					"Actually included: %v\n",
					tt.scenario,
					tt.prClosedAt.Format(time.RFC3339),
					tt.pollingRunsAt.Format(time.RFC3339),
					tt.lookbackHours,
					windowStart.Format(time.RFC3339),
					tt.pollingRunsAt.Sub(tt.prClosedAt),
					tt.expectPRIncluded,
					prIncluded)
			}

			// Document the bug explicitly
			if !tt.expectPRIncluded && tt.lookbackHours == 1 {
				t.Logf("✅ TEST CONFIRMS BUG: PR closed %v ago will be PERMANENTLY MISSED with 1h lookback window",
					tt.pollingRunsAt.Sub(tt.prClosedAt))
			}
		})
	}
}

// TestClosedPRRecoveryScenarios tests various sprinkler outage recovery scenarios.
func TestClosedPRRecoveryScenarios(t *testing.T) {
	scenarios := []struct {
		name              string
		sprinklerDownAt   time.Time
		prClosedAt        time.Time
		sprinklerUpAt     time.Time
		pollingInterval   time.Duration
		lookbackWindow    time.Duration
		expectRecovery    bool
		recoveryMechanism string
	}{
		{
			name:              "30-minute outage - polling catches it",
			sprinklerDownAt:   parseTime("10:00"),
			prClosedAt:        parseTime("10:15"),
			sprinklerUpAt:     parseTime("10:30"),
			pollingInterval:   5 * time.Minute,
			lookbackWindow:    1 * time.Hour,
			expectRecovery:    true,
			recoveryMechanism: "Next poll at 10:35 catches PR (closed 20min ago)",
		},
		{
			name:              "90-minute outage - PERMANENT LOSS (current bug)",
			sprinklerDownAt:   parseTime("10:00"),
			prClosedAt:        parseTime("10:30"),
			sprinklerUpAt:     parseTime("11:30"),
			pollingInterval:   5 * time.Minute,
			lookbackWindow:    1 * time.Hour,
			expectRecovery:    false, // BUG: PR closed 1h+ ago, outside window
			recoveryMechanism: "NONE - PR closed 60min+ ago is outside 1h lookback window",
		},
		{
			name:              "2-hour outage - PERMANENT LOSS (current bug)",
			sprinklerDownAt:   parseTime("10:00"),
			prClosedAt:        parseTime("10:30"),
			sprinklerUpAt:     parseTime("12:00"),
			pollingInterval:   5 * time.Minute,
			lookbackWindow:    1 * time.Hour,
			expectRecovery:    false, // BUG: PR closed 90min+ ago
			recoveryMechanism: "NONE - even when sprinkler returns, polling can't recover",
		},
		{
			name:              "2-hour outage - WOULD RECOVER with 24h window",
			sprinklerDownAt:   parseTime("10:00"),
			prClosedAt:        parseTime("10:30"),
			sprinklerUpAt:     parseTime("12:00"),
			pollingInterval:   5 * time.Minute,
			lookbackWindow:    24 * time.Hour,
			expectRecovery:    true, // Fixed: 24h window catches it
			recoveryMechanism: "Next poll catches PR (closed 90min ago, within 24h window)",
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			// First poll after sprinkler comes back up
			firstPollAfterRecovery := sc.sprinklerUpAt.Add(sc.pollingInterval)
			timeSinceClose := firstPollAfterRecovery.Sub(sc.prClosedAt)

			// Check if PR would be in lookback window
			wouldBeCaught := timeSinceClose <= sc.lookbackWindow

			if wouldBeCaught != sc.expectRecovery {
				t.Errorf("SCENARIO: %s\n"+
					"Sprinkler down:  %s\n"+
					"PR closed:       %s\n"+
					"Sprinkler up:    %s\n"+
					"First poll:      %s\n"+
					"Time since close: %v\n"+
					"Lookback window:  %v\n"+
					"Expected recovery: %v\n"+
					"Actual recovery:   %v\n"+
					"Mechanism: %s\n",
					sc.name,
					sc.sprinklerDownAt.Format("15:04"),
					sc.prClosedAt.Format("15:04"),
					sc.sprinklerUpAt.Format("15:04"),
					firstPollAfterRecovery.Format("15:04"),
					timeSinceClose,
					sc.lookbackWindow,
					sc.expectRecovery,
					wouldBeCaught,
					sc.recoveryMechanism)
			}

			// Document bug cases
			if !sc.expectRecovery && sc.lookbackWindow == 1*time.Hour {
				t.Logf("✅ TEST CONFIRMS BUG: %s - Update permanently lost", sc.name)
			}

			// Document fix validation
			if sc.expectRecovery && sc.lookbackWindow == 24*time.Hour && timeSinceClose > 1*time.Hour {
				t.Logf("✅ TEST VALIDATES FIX: 24h window would recover this scenario")
			}
		})
	}
}

// parseTime is a helper to create times on today's date for testing.
func parseTime(hhMM string) time.Time {
	now := time.Now()
	parsed, err := time.Parse("15:04", hhMM)
	if err != nil {
		// This should never happen in tests with valid time strings
		panic("parseTime: invalid time format " + hhMM + ": " + err.Error())
	}
	return time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location())
}
