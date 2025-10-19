// Package home implements the Slack app home dashboard.
package home

import (
	"time"
)

// PR represents a pull request for display in the home dashboard.
type PR struct {
	UpdatedAt     time.Time // Last time PR metadata was updated
	LastEventTime time.Time // Last sprinkler event for turnclient cache optimization
	Title         string
	URL           string
	Repository    string // "owner/repo" format
	Author        string // GitHub username
	ActionReason  string // Human-readable reason from turnclient
	ActionKind    string // Action type: review, merge, fix_tests, etc.
	TestState     string // Test state: running, passing, failing
	Number        int
	IsDraft       bool
	IsBlocked     bool // Outgoing PRs - blocked on author
	NeedsReview   bool // Incoming PRs - needs user's review
}

// Dashboard contains all data needed to render the home tab.
type Dashboard struct {
	IncomingPRs   []PR
	OutgoingPRs   []PR
	WorkspaceOrgs []string // Organizations tracked by this workspace
}

// PRCounts summarizes PR counts for display.
type PRCounts struct {
	IncomingTotal   int
	IncomingBlocked int
	OutgoingTotal   int
	OutgoingBlocked int
}

// Counts returns summary counts for the dashboard.
func (d *Dashboard) Counts() PRCounts {
	var counts PRCounts

	counts.IncomingTotal = len(d.IncomingPRs)
	counts.OutgoingTotal = len(d.OutgoingPRs)

	for i := range d.IncomingPRs {
		if d.IncomingPRs[i].NeedsReview {
			counts.IncomingBlocked++
		}
	}

	for i := range d.OutgoingPRs {
		if d.OutgoingPRs[i].IsBlocked {
			counts.OutgoingBlocked++
		}
	}

	return counts
}
