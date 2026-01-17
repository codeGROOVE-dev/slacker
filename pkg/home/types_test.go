package home

import (
	"testing"
)

func TestDashboard_Counts(t *testing.T) {
	dashboard := &Dashboard{
		IncomingPRs: []PR{
			{
				Title:       "PR 1",
				NeedsReview: true, // Blocked incoming
			},
			{
				Title:       "PR 2",
				NeedsReview: false,
			},
			{
				Title:       "PR 3",
				NeedsReview: true, // Blocked incoming
			},
		},
		OutgoingPRs: []PR{
			{
				Title:     "PR 4",
				IsBlocked: true, // Blocked outgoing
			},
			{
				Title:     "PR 5",
				IsBlocked: false,
			},
		},
	}

	counts := dashboard.Counts()

	if counts.IncomingTotal != 3 {
		t.Errorf("expected 3 incoming PRs, got %d", counts.IncomingTotal)
	}

	if counts.IncomingBlocked != 2 {
		t.Errorf("expected 2 blocked incoming PRs, got %d", counts.IncomingBlocked)
	}

	if counts.OutgoingTotal != 2 {
		t.Errorf("expected 2 outgoing PRs, got %d", counts.OutgoingTotal)
	}

	if counts.OutgoingBlocked != 1 {
		t.Errorf("expected 1 blocked outgoing PR, got %d", counts.OutgoingBlocked)
	}
}

func TestDashboard_Counts_Empty(t *testing.T) {
	dashboard := &Dashboard{
		IncomingPRs: []PR{},
		OutgoingPRs: []PR{},
	}

	counts := dashboard.Counts()

	if counts.IncomingTotal != 0 {
		t.Errorf("expected 0 incoming PRs, got %d", counts.IncomingTotal)
	}

	if counts.IncomingBlocked != 0 {
		t.Errorf("expected 0 blocked incoming PRs, got %d", counts.IncomingBlocked)
	}

	if counts.OutgoingTotal != 0 {
		t.Errorf("expected 0 outgoing PRs, got %d", counts.OutgoingTotal)
	}

	if counts.OutgoingBlocked != 0 {
		t.Errorf("expected 0 blocked outgoing PRs, got %d", counts.OutgoingBlocked)
	}
}
