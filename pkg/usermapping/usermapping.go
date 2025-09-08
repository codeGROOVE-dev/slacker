// Package usermapping provides GitHub-to-Slack user mapping functionality.
package usermapping

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	ghmailto "github.com/codeGROOVE-dev/gh-mailto/pkg/gh-mailto"
	"github.com/slack-go/slack"
)

// Constants for caching and matching.
const (
	cacheTTL             = 24 * time.Hour // Cache mappings for 24 hours
	maxConcurrentLookups = 5              // Limit concurrent email lookups
)

// UserMapping represents a GitHub-to-Slack user mapping.
type UserMapping struct {
	GitHubUsername string
	SlackUserID    string
	SlackUsername  string
	MatchedEmail   string
	ChannelContext string // Channel where mapping was established
	Confidence     int    // Match confidence (0-100)
	CachedAt       time.Time
}

// Service handles GitHub-to-Slack user mapping.
type Service struct {
	slackClient  *slack.Client
	githubLookup *ghmailto.Lookup
	cache        map[string]*UserMapping
	cacheMu      sync.RWMutex
	lookupSem    chan struct{} // Semaphore for limiting concurrent lookups
}

// New creates a new user mapping service.
func New(slackClient *slack.Client, githubToken string) *Service {
	return &Service{
		slackClient:  slackClient,
		githubLookup: ghmailto.New(githubToken),
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, maxConcurrentLookups),
	}
}

// GetSlackHandle attempts to find a Slack handle for a GitHub username.
// It uses email matching with preference for users in the specified channel.
func (s *Service) GetSlackHandle(ctx context.Context, githubUsername, channelID string) (string, error) {
	// Check cache first
	if mapping := s.getCachedMapping(githubUsername, channelID); mapping != nil {
		slog.Debug("using cached GitHub-to-Slack mapping",
			"github_user", githubUsername,
			"slack_user", mapping.SlackUsername,
			"channel", channelID,
			"confidence", mapping.Confidence,
			"age_hours", time.Since(mapping.CachedAt).Hours())
		return mapping.SlackUsername, nil
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
		"channel", channelID)

	// Get emails for GitHub user - we need to infer organization from context
	// For now, we'll do a lookup without specific organization (may be less accurate)
	result, err := s.githubLookup.Lookup(ctx, githubUsername, "")
	if err != nil {
		slog.Warn("failed to get emails for GitHub user",
			"github_user", githubUsername,
			"error", err)
		return "", err
	}

	if len(result.Addresses) == 0 {
		slog.Info("no emails found for GitHub user",
			"github_user", githubUsername)
		// Cache negative result to avoid repeated lookups
		s.cacheMapping(&UserMapping{
			GitHubUsername: githubUsername,
			SlackUserID:    "",
			SlackUsername:  "",
			ChannelContext: channelID,
			Confidence:     0,
			CachedAt:       time.Now(),
		})
		return "", nil
	}

	// Extract email addresses from results
	emails := make([]string, len(result.Addresses))
	for i, addr := range result.Addresses {
		emails[i] = addr.Email
	}

	slog.Debug("found emails for GitHub user",
		"github_user", githubUsername,
		"email_count", len(emails),
		"emails", emails)

	// Find matching Slack users
	matches, err := s.findSlackMatches(ctx, emails, channelID)
	if err != nil {
		slog.Error("failed to find Slack matches",
			"github_user", githubUsername,
			"error", err)
		return "", err
	}

	if len(matches) == 0 {
		slog.Info("no Slack users found matching GitHub emails",
			"github_user", githubUsername,
			"emails_checked", len(emails))
		// Cache negative result
		s.cacheMapping(&UserMapping{
			GitHubUsername: githubUsername,
			SlackUserID:    "",
			SlackUsername:  "",
			ChannelContext: channelID,
			Confidence:     0,
			CachedAt:       time.Now(),
		})
		return "", nil
	}

	// Select best match (highest confidence, prefer channel members)
	bestMatch := s.selectBestMatch(matches, channelID)

	slog.Info("successfully mapped GitHub user to Slack",
		"github_user", githubUsername,
		"slack_user", bestMatch.SlackUsername,
		"slack_user_id", bestMatch.SlackUserID,
		"matched_email", bestMatch.MatchedEmail,
		"confidence", bestMatch.Confidence,
		"channel_member", bestMatch.ChannelContext == channelID)

	// Cache the mapping
	s.cacheMapping(bestMatch)

	return bestMatch.SlackUsername, nil
}

// GetSlackHandles performs batch lookup of multiple GitHub users to Slack handles.
// Returns a map of GitHub username to Slack handle (empty string if not found).
func (s *Service) GetSlackHandles(ctx context.Context, githubUsernames []string, channelID string) (map[string]string, error) {
	if len(githubUsernames) == 0 {
		return make(map[string]string), nil
	}

	slog.Debug("performing batch GitHub-to-Slack user lookup",
		"github_users", githubUsernames,
		"user_count", len(githubUsernames),
		"channel", channelID)

	results := make(map[string]string, len(githubUsernames))

	// Process users concurrently but respect semaphore limits
	for _, username := range githubUsernames {
		slackHandle, err := s.GetSlackHandle(ctx, username, channelID)
		if err != nil {
			slog.Warn("failed to lookup Slack handle for GitHub user",
				"github_user", username,
				"error", err)
			results[username] = "" // Store empty string for failed lookups
		} else {
			results[username] = slackHandle
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
func (s *Service) FormatUserMention(ctx context.Context, githubUsername, channelID string) string {
	if githubUsername == "" {
		return ""
	}

	slackHandle, err := s.GetSlackHandle(ctx, githubUsername, channelID)
	if err != nil || slackHandle == "" {
		slog.Debug("falling back to GitHub username for mention",
			"github_user", githubUsername,
			"channel", channelID,
			"reason", "no_slack_mapping")
		return "@" + githubUsername
	}

	return "<@" + slackHandle + ">"
}

// FormatUserMentions formats multiple GitHub usernames as Slack mentions.
// Returns formatted string like "@slackUser1, @slackUser2" or falls back to GitHub usernames.
func (s *Service) FormatUserMentions(ctx context.Context, githubUsernames []string, channelID string) string {
	if len(githubUsernames) == 0 {
		return ""
	}

	handles, err := s.GetSlackHandles(ctx, githubUsernames, channelID)
	if err != nil {
		slog.Warn("failed to get Slack handles for batch formatting",
			"github_users", githubUsernames,
			"error", err)
	}

	var mentions []string
	for _, username := range githubUsernames {
		if handle, exists := handles[username]; exists && handle != "" {
			mentions = append(mentions, "<@"+handle+">")
		} else {
			mentions = append(mentions, "@"+username)
		}
	}

	return strings.Join(mentions, ", ")
}

// getCachedMapping retrieves a cached mapping, preferring channel-specific ones.
func (s *Service) getCachedMapping(githubUsername, channelID string) *UserMapping {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	// First try channel-specific cache key
	channelKey := githubUsername + ":" + channelID
	if mapping, exists := s.cache[channelKey]; exists {
		if time.Since(mapping.CachedAt) < cacheTTL {
			return mapping
		}
		// Expired, remove it
		delete(s.cache, channelKey)
	}

	// Fall back to general mapping
	if mapping, exists := s.cache[githubUsername]; exists {
		if time.Since(mapping.CachedAt) < cacheTTL {
			return mapping
		}
		// Expired, remove it
		delete(s.cache, githubUsername)
	}

	return nil
}

// cacheMapping stores a mapping in the cache.
func (s *Service) cacheMapping(mapping *UserMapping) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	// Store with channel context if available
	if mapping.ChannelContext != "" {
		key := mapping.GitHubUsername + ":" + mapping.ChannelContext
		s.cache[key] = mapping
	}

	// Also store general mapping
	s.cache[mapping.GitHubUsername] = mapping
}

// findSlackMatches finds Slack users matching the given emails.
func (s *Service) findSlackMatches(ctx context.Context, emails []string, channelID string) ([]*UserMapping, error) {
	var matches []*UserMapping

	// Get channel members for preference scoring
	var channelMembers map[string]bool
	if channelID != "" {
		members, err := s.getChannelMembers(ctx, channelID)
		if err != nil {
			slog.Warn("failed to get channel members, will match against all users",
				"channel", channelID,
				"error", err)
		} else {
			channelMembers = members
		}
	}

	// Search for each email in Slack user directory
	for _, email := range emails {
		user, err := s.slackClient.GetUserByEmailContext(ctx, email)
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
		confidence := s.calculateConfidence(user, email, channelMembers)

		mapping := &UserMapping{
			SlackUserID:   user.ID,
			SlackUsername: user.Name,
			MatchedEmail:  email,
			Confidence:    confidence,
			CachedAt:      time.Now(),
		}

		// Set channel context if user is in the channel
		if channelMembers != nil && channelMembers[user.ID] {
			mapping.ChannelContext = channelID
		}

		matches = append(matches, mapping)

		slog.Debug("found Slack user match",
			"email", email,
			"slack_user", user.Name,
			"confidence", confidence,
			"in_channel", channelMembers != nil && channelMembers[user.ID])
	}

	return matches, nil
}

// getChannelMembers retrieves the members of a channel.
func (s *Service) getChannelMembers(ctx context.Context, channelID string) (map[string]bool, error) {
	members, _, err := s.slackClient.GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
		ChannelID: channelID,
	})
	if err != nil {
		return nil, err
	}

	memberMap := make(map[string]bool, len(members))
	for _, memberID := range members {
		memberMap[memberID] = true
	}

	return memberMap, nil
}

// calculateConfidence calculates a confidence score for a user match.
func (s *Service) calculateConfidence(user *slack.User, email string, channelMembers map[string]bool) int {
	confidence := 50 // Base confidence for email match

	// Boost confidence if user is in the relevant channel
	if channelMembers != nil && channelMembers[user.ID] {
		confidence += 30
	}

	// Boost confidence for primary email vs secondary
	if user.Profile.Email == email {
		confidence += 20
	}

	// Reduce confidence for generic/shared emails
	if isGenericEmail(email) {
		confidence -= 20
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
func (s *Service) selectBestMatch(matches []*UserMapping, channelID string) *UserMapping {
	if len(matches) == 0 {
		return nil
	}

	best := matches[0]
	for _, match := range matches[1:] {
		// Prefer channel members
		if match.ChannelContext == channelID && best.ChannelContext != channelID {
			best = match
			continue
		}
		if best.ChannelContext == channelID && match.ChannelContext != channelID {
			continue
		}

		// Among equal channel membership, prefer higher confidence
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
