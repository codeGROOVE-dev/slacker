package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/slacker/internal/github"
)

// ConfigProvider provides configuration for daily digests.
type ConfigProvider interface {
	DailyRemindersEnabled(org string) bool
	Domain(org string) string
}

// StateProvider provides state storage for daily digests.
type StateProvider interface {
	GetLastDigest(userID, date string) (time.Time, bool)
	RecordDigest(userID, date string, sentAt time.Time) error
	GetLastDM(userID, prURL string) (time.Time, bool)
}

// DailyDigestScheduler handles sending daily digest DMs to users blocking PRs.
type DailyDigestScheduler struct {
	notifier      *Manager
	githubManager *github.Manager
	configManager ConfigProvider
	stateStore    StateProvider
}

// NewDailyDigestScheduler creates a new daily digest scheduler.
func NewDailyDigestScheduler(
	notifier *Manager,
	githubManager *github.Manager,
	configManager ConfigProvider,
	stateStore StateProvider,
) *DailyDigestScheduler {
	return &DailyDigestScheduler{
		notifier:      notifier,
		githubManager: githubManager,
		configManager: configManager,
		stateStore:    stateStore,
	}
}

// CheckAndSend checks all users and sends daily digests to those in the 8-9am window.
// Runs hourly to catch users across all timezones.
func (d *DailyDigestScheduler) CheckAndSend(ctx context.Context) {
	slog.Info("checking for daily digest candidates",
		"check_time", time.Now().Format(time.RFC3339))

	orgs := d.githubManager.AllOrgs()
	if len(orgs) == 0 {
		slog.Debug("no organizations configured, skipping daily digest check")
		return
	}

	totalSent := 0
	totalSkipped := 0
	totalErrors := 0

	for _, org := range orgs {
		// Check if daily reminders are enabled for this org
		if !d.configManager.DailyRemindersEnabled(org) {
			slog.Debug("daily reminders disabled for org", "org", org)
			continue
		}

		// TODO: Implement full daily digest logic
		// For now, log that we would process this org
		slog.Debug("daily digest check for org (full implementation pending)",
			"org", org)
		totalSkipped++
	}

	slog.Info("daily digest check complete",
		"orgs_checked", len(orgs),
		"digests_sent", totalSent,
		"skipped", totalSkipped,
		"errors", totalErrors)
}

// TODO: Implement full daily digest functionality.
// The remaining implementation will require:
// - User mapping from GitHub to Slack
// - PR analysis with turnclient
// - Timezone-aware message delivery
// - Deduplication with existing DM notifications
