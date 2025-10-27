// Package usermapping provides GitHub-to-Slack user mapping functionality.
package usermapping

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	ghmailto "github.com/codeGROOVE-dev/gh-mailto/pkg/gh-mailto"
	"github.com/slack-go/slack"
	"golang.org/x/sync/singleflight"
)

// Constants for caching and matching.
const (
	cacheTTL             = 24 * time.Hour // Cache mappings for 24 hours
	maxConcurrentLookups = 5              // Limit concurrent email lookups

	// Confidence scoring constants.
	baseConfidence      = 50 // Base confidence for email match
	channelMemberBonus  = 30 // Bonus for users in the target channel
	primaryEmailBonus   = 20 // Bonus for primary email match
	genericEmailPenalty = 20 // Penalty for generic/shared emails
)

// UserMapping represents a GitHub-to-Slack user mapping.
type UserMapping struct {
	CachedAt       time.Time
	GitHubUsername string
	SlackUserID    string
	SlackUsername  string
	MatchedEmail   string
	Confidence     int // Match confidence (0-100)
}

// SlackAPI defines the interface for Slack API operations needed by the mapping service.
type SlackAPI interface {
	GetUserByEmailContext(ctx context.Context, email string) (*slack.User, error)
}

// GitHubEmailLookup defines the interface for GitHub email lookup operations.
type GitHubEmailLookup interface {
	Lookup(ctx context.Context, username, organization string) (*ghmailto.Result, error)
	Guess(ctx context.Context, username, organization string, opts ghmailto.GuessOptions) (*ghmailto.GuessResult, error)
}

// Service handles GitHub-to-Slack user mapping.
//
//nolint:govet // Field order optimized for logical grouping over memory alignment
type Service struct {
	cacheMu      sync.RWMutex
	flight       singleflight.Group // Deduplicates concurrent identical lookups
	slackClient  SlackAPI
	githubLookup GitHubEmailLookup
	cache        map[string]*UserMapping
	lookupSem    chan struct{} // Semaphore for limiting concurrent lookups
}

// New creates a new user mapping service.
func New(slackClient *slack.Client, githubToken string) *Service {
	return &Service{
		slackClient: slackClient,
		githubLookup: ghmailto.New(githubToken,
			ghmailto.WithCommitsLimit(25), // Limit commit search for performance
		),
		cache:     make(map[string]*UserMapping),
		lookupSem: make(chan struct{}, maxConcurrentLookups),
	}
}

// SlackHandle attempts to find a Slack user ID for a GitHub username.
// It uses email matching to find the best Slack user match and returns the Slack user ID (e.g., "U1234567890").
func (s *Service) SlackHandle(ctx context.Context, githubUsername, organization, domain string) (string, error) {
	// Check cache first
	if mapping := s.getCachedMapping(githubUsername); mapping != nil {
		slog.Debug("using cached GitHub-to-Slack mapping",
			"github_user", githubUsername,
			"slack_user_id", mapping.SlackUserID,
			"slack_username", mapping.SlackUsername,
			"confidence", mapping.Confidence,
			"age_hours", time.Since(mapping.CachedAt).Hours())
		return mapping.SlackUserID, nil
	}

	// Use singleflight to deduplicate concurrent identical lookups
	// Key includes username to ensure we deduplicate the same user
	// Multiple goroutines requesting the same user will share one lookup
	key := githubUsername
	result, err, shared := s.flight.Do(key, func() (any, error) {
		// Only one goroutine per key will execute this function
		// Others will wait and receive the same result

		// Double-check cache (another goroutine might have completed while we waited)
		if mapping := s.getCachedMapping(githubUsername); mapping != nil {
			slog.Debug("using cached mapping after singleflight wait",
				"github_user", githubUsername,
				"slack_user_id", mapping.SlackUserID)
			return mapping.SlackUserID, nil
		}

		// Acquire semaphore to limit concurrent lookups
		select {
		case s.lookupSem <- struct{}{}:
			defer func() { <-s.lookupSem }()
		case <-ctx.Done():
			return "", ctx.Err()
		}

		slog.Debug("performing GitHub-to-Slack user lookup",
			"github_user", githubUsername,
			"organization", organization,
			"domain", domain)

		return s.doLookup(ctx, githubUsername, organization, domain)
	})

	if err != nil {
		return "", err
	}

	slackUserID, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("unexpected result type from singleflight: %T", result)
	}
	if shared {
		slog.Debug("reused concurrent lookup result via singleflight",
			"github_user", githubUsername,
			"slack_user_id", slackUserID)
	}

	return slackUserID, nil
}

// doLookup performs the actual email lookup and mapping logic.
// Extracted from SlackHandle to work with singleflight pattern.
func (s *Service) doLookup(ctx context.Context, githubUsername, organization, domain string) (string, error) {
	// Get emails for GitHub user with organization context
	result, err := s.githubLookup.Lookup(ctx, githubUsername, organization)
	if err != nil {
		slog.Warn("failed to get emails for GitHub user",
			"github_user", githubUsername,
			"error", err)
		return "", err
	}

	// First try all found emails (including any domain) for direct matches
	var emails []string
	if len(result.Addresses) > 0 {
		emails = make([]string, len(result.Addresses))
		for i, addr := range result.Addresses {
			emails[i] = addr.Email
		}

		slog.Debug("found emails for GitHub user",
			"github_user", githubUsername,
			"email_count", len(emails),
			"emails", emails)

		// Try finding Slack matches with all emails first
		matches := s.findSlackMatches(ctx, githubUsername, emails)
		if len(matches) > 0 {
			bestMatch := s.selectBestMatch(matches)
			slog.Info("successfully mapped GitHub user to Slack via direct email match",
				"github_user", githubUsername,
				"slack_user_id", bestMatch.SlackUserID,
				"slack_username", bestMatch.SlackUsername,
				"matched_email", bestMatch.MatchedEmail,
				"confidence", bestMatch.Confidence)
			s.cacheMapping(bestMatch)
			return bestMatch.SlackUserID, nil
		}
	}

	// If no direct matches and domain is specified, try domain-specific approaches
	if domain != "" {
		// Try normalized emails within the target domain
		if len(result.Addresses) > 0 {
			normalizedResult := result.FilterAndNormalize(ghmailto.FilterOptions{
				Domain: domain,
			})

			if len(normalizedResult.Addresses) > 0 {
				normalizedEmails := make([]string, len(normalizedResult.Addresses))
				for i, addr := range normalizedResult.Addresses {
					normalizedEmails[i] = addr.Email
				}

				slog.Debug("trying normalized domain emails",
					"github_user", githubUsername,
					"domain", domain,
					"normalized_emails", normalizedEmails)

				matches := s.findSlackMatches(ctx, githubUsername, normalizedEmails)
				if len(matches) > 0 {
					bestMatch := s.selectBestMatch(matches)
					slog.Info("successfully mapped GitHub user to Slack via normalized email",
						"github_user", githubUsername,
						"slack_user_id", bestMatch.SlackUserID,
						"slack_username", bestMatch.SlackUsername,
						"matched_email", bestMatch.MatchedEmail,
						"confidence", bestMatch.Confidence)
					s.cacheMapping(bestMatch)
					return bestMatch.SlackUserID, nil
				}
			}
		}
		// Finally, try intelligent guessing
		slog.Debug("trying intelligent email guessing",
			"github_user", githubUsername,
			"domain", domain)

		guessResult, err := s.githubLookup.Guess(ctx, githubUsername, organization, ghmailto.GuessOptions{
			Domain: domain,
		})
		if err != nil {
			slog.Warn("email guessing failed",
				"github_user", githubUsername,
				"domain", domain,
				"error", err)
		} else if len(guessResult.Guesses) > 0 {
			guessedEmails := make([]string, len(guessResult.Guesses))
			for i, addr := range guessResult.Guesses {
				guessedEmails[i] = addr.Email
			}

			slog.Debug("generated email guesses",
				"github_user", githubUsername,
				"domain", domain,
				"guesses", guessedEmails)

			matches := s.findSlackMatches(ctx, githubUsername, guessedEmails)
			if len(matches) > 0 {
				bestMatch := s.selectBestMatch(matches)
				slog.Info("successfully mapped GitHub user to Slack via email guessing",
					"github_user", githubUsername,
					"slack_user_id", bestMatch.SlackUserID,
					"slack_username", bestMatch.SlackUsername,
					"guessed_email", bestMatch.MatchedEmail,
					"confidence", bestMatch.Confidence)
				s.cacheMapping(bestMatch)
				return bestMatch.SlackUserID, nil
			}
		}
	}

	// No matches found through any method
	slog.Info("no Slack mapping found for GitHub user",
		"github_user", githubUsername,
		"tried_direct_emails", len(emails) > 0,
		"tried_domain_filtering", domain != "",
		"tried_guessing", domain != "")

	// Cache negative result to avoid repeated lookups
	s.cacheMapping(&UserMapping{
		GitHubUsername: githubUsername,
		SlackUserID:    "",
		SlackUsername:  "",
		Confidence:     0,
		CachedAt:       time.Now(),
	})
	return "", nil
}

// SlackHandles performs batch lookup of multiple GitHub users to Slack user IDs.
// Returns a map of GitHub username to Slack user ID (empty string if not found).
func (s *Service) SlackHandles(ctx context.Context, githubUsernames []string, organization, domain string) (map[string]string, error) {
	if len(githubUsernames) == 0 {
		return make(map[string]string), nil
	}

	slog.Debug("performing batch GitHub-to-Slack user lookup",
		"github_users", githubUsernames,
		"user_count", len(githubUsernames),
		"organization", organization,
		"domain", domain)

	results := make(map[string]string, len(githubUsernames))

	// Process users concurrently but respect semaphore limits
	for _, username := range githubUsernames {
		slackUserID, err := s.SlackHandle(ctx, username, organization, domain)
		if err != nil {
			slog.Warn("failed to lookup Slack user ID for GitHub user",
				"github_user", username,
				"error", err)
			results[username] = "" // Store empty string for failed lookups
		} else {
			results[username] = slackUserID
		}
	}

	slog.Debug("completed batch GitHub-to-Slack lookup",
		"github_users", githubUsernames,
		"successful_mappings", func() int {
			count := 0
			for _, handle := range results {
				if handle != "" {
					count++
				}
			}
			return count
		}())

	return results, nil
}

// FormatUserMention formats a GitHub username as a Slack mention if mapping exists.
// Falls back to @githubUsername if no Slack mapping is found.
func (s *Service) FormatUserMention(ctx context.Context, githubUsername, organization, domain string) string {
	if githubUsername == "" {
		return ""
	}

	slackUserID, err := s.SlackHandle(ctx, githubUsername, organization, domain)
	if err != nil || slackUserID == "" {
		slog.Debug("falling back to GitHub username for mention",
			"github_user", githubUsername,
			"reason", "no_slack_mapping")
		return "@" + githubUsername
	}

	return "<@" + slackUserID + ">"
}

// FormatUserMentions formats multiple GitHub usernames as Slack mentions.
// Returns formatted string like "@slackUser1, @slackUser2" or falls back to GitHub usernames.
func (s *Service) FormatUserMentions(ctx context.Context, githubUsernames []string, organization, domain string) string {
	if len(githubUsernames) == 0 {
		return ""
	}

	userIDs, err := s.SlackHandles(ctx, githubUsernames, organization, domain)
	if err != nil {
		slog.Warn("failed to get Slack user IDs for batch formatting",
			"github_users", githubUsernames,
			"error", err)
	}

	var mentions []string
	for _, username := range githubUsernames {
		if userID, exists := userIDs[username]; exists && userID != "" {
			mentions = append(mentions, "<@"+userID+">")
		} else {
			mentions = append(mentions, "@"+username)
		}
	}

	return strings.Join(mentions, ", ")
}

// getCachedMapping retrieves a cached mapping.
func (s *Service) getCachedMapping(githubUsername string) *UserMapping {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	if mapping, exists := s.cache[githubUsername]; exists {
		if time.Since(mapping.CachedAt) < cacheTTL {
			return mapping
		}
		// Expired, remove it lazily (we'll clean it up next time we cache)
	}

	return nil
}

// cacheMapping stores a mapping in the cache.
func (s *Service) cacheMapping(mapping *UserMapping) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	// Clean up expired entries while we have the write lock
	now := time.Now()
	for key, cached := range s.cache {
		if now.Sub(cached.CachedAt) >= cacheTTL {
			delete(s.cache, key)
		}
	}

	// Store the mapping
	s.cache[mapping.GitHubUsername] = mapping
}

// findSlackMatches finds Slack users matching the given emails.
func (s *Service) findSlackMatches(ctx context.Context, githubUsername string, emails []string) []*UserMapping {
	var matches []*UserMapping

	// Search for each email in Slack user directory
	for _, email := range emails {
		// Normalize email to lowercase for consistent matching
		normalizedEmail := strings.ToLower(email)
		user, err := s.slackClient.GetUserByEmailContext(ctx, normalizedEmail)
		if err != nil {
			slog.Debug("no Slack user found for email",
				"email", email,
				"error", err)
			continue
		}

		if user.Deleted {
			slog.Debug("skipping deleted Slack user",
				"email", email,
				"user_id", user.ID)
			continue
		}

		// Calculate confidence score
		confidence := s.calculateConfidence(user, email)

		mapping := &UserMapping{
			GitHubUsername: githubUsername,
			SlackUserID:    user.ID,
			SlackUsername:  user.Name,
			MatchedEmail:   email,
			Confidence:     confidence,
			CachedAt:       time.Now(),
		}

		matches = append(matches, mapping)

		slog.Debug("found Slack user match",
			"email", email,
			"slack_user", user.Name,
			"confidence", confidence)
	}

	return matches
}

// calculateConfidence calculates a confidence score for a user match.
func (*Service) calculateConfidence(user *slack.User, email string) int {
	confidence := baseConfidence // Base confidence for email match

	// Boost confidence for primary email vs secondary
	if user.Profile.Email == email {
		confidence += primaryEmailBonus
	}

	// Reduce confidence for generic/shared emails
	if isGenericEmail(email) {
		confidence -= genericEmailPenalty
	}

	// Ensure confidence is within bounds
	if confidence > 100 {
		confidence = 100
	}
	if confidence < 0 {
		confidence = 0
	}

	return confidence
}

// selectBestMatch selects the best match from multiple candidates.
func (*Service) selectBestMatch(matches []*UserMapping) *UserMapping {
	if len(matches) == 0 {
		return nil
	}

	best := matches[0]
	for _, match := range matches[1:] {
		// Prefer higher confidence
		if match.Confidence > best.Confidence {
			best = match
		}
	}

	return best
}

// isGenericEmail checks if an email looks generic/shared.
func isGenericEmail(email string) bool {
	genericPrefixes := []string{
		"noreply", "no-reply", "donotreply", "admin", "info", "support",
		"help", "contact", "team", "hello", "hi", "bot", "system",
	}

	emailLower := strings.ToLower(email)
	for _, prefix := range genericPrefixes {
		if strings.HasPrefix(emailLower, prefix+"@") {
			return true
		}
	}

	return false
}

// ClearCache clears the user mapping cache.
func (s *Service) ClearCache() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cache = make(map[string]*UserMapping)
	slog.Info("cleared user mapping cache")
}

// CacheStats returns cache statistics.
func (s *Service) CacheStats() (total int, expired int) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	total = len(s.cache)
	now := time.Now()
	for _, mapping := range s.cache {
		if now.Sub(mapping.CachedAt) >= cacheTTL {
			expired++
		}
	}

	return total, expired
}
