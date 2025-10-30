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
)

// ReverseService handles Slack-to-GitHub user mapping (reverse of the main Service).
type ReverseService struct {
	orgCache  *ghmailto.OrgCacheService
	cache     map[string]*ReverseMapping // slackUserID -> ReverseMapping
	overrides map[string]string          // githubUsername -> email (from config)
	cacheMu   sync.RWMutex
}

// ReverseMapping represents a Slack-to-GitHub user mapping.
type ReverseMapping struct {
	CachedAt       time.Time
	SlackUserID    string
	SlackUsername  string
	SlackEmail     string
	GitHubUsername string
	MatchMethod    string // "email_lookup", "guess", "config_override"
	Confidence     int    // Match confidence (0-100)
}

// NewReverseService creates a new reverse mapping service (Slack → GitHub).
func NewReverseService(slackClient *slack.Client, githubToken string) *ReverseService {
	return &ReverseService{
		orgCache:  ghmailto.NewOrgCacheService(githubToken),
		cache:     make(map[string]*ReverseMapping),
		overrides: make(map[string]string),
	}
}

// SetOverrides updates the manual user mapping overrides from config.
// Format: githubUsername -> email
func (s *ReverseService) SetOverrides(overrides map[string]string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.overrides = overrides
	slog.Info("updated user mapping overrides", "count", len(overrides))
}

// LookupGitHub attempts to find a GitHub username for a Slack user ID.
// It uses email matching via gh-mailto to find the best GitHub username match.
func (s *ReverseService) LookupGitHub(ctx context.Context, slackClient SlackAPI, slackUserID, organization, domain string) (*ReverseMapping, error) {
	// Check cache first
	if m := s.cachedMapping(slackUserID); m != nil {
		slog.Debug("using cached Slack-to-GitHub mapping",
			"slack_user_id", slackUserID,
			"github_username", m.GitHubUsername,
			"confidence", m.Confidence,
			"age_hours", time.Since(m.CachedAt).Hours())
		return m, nil
	}

	slog.Debug("performing Slack-to-GitHub user lookup",
		"slack_user_id", slackUserID,
		"organization", organization,
		"domain", domain)

	// Get Slack user info to extract email
	slackUser, err := slackClient.GetUserInfo(slackUserID)
	if err != nil {
		slog.Warn("failed to get Slack user info",
			"slack_user_id", slackUserID,
			"error", err)
		return nil, fmt.Errorf("failed to get Slack user info: %w", err)
	}

	email := slackUser.Profile.Email
	if email == "" {
		slog.Warn("Slack user has no email",
			"slack_user_id", slackUserID,
			"slack_username", slackUser.Name)
		return nil, fmt.Errorf("Slack user has no email: %s", slackUserID)
	}

	// Check if this email has a config override
	for githubUsername, overrideEmail := range s.overrides {
		if strings.EqualFold(overrideEmail, email) {
			slog.Info("using config override for user mapping",
				"slack_user_id", slackUserID,
				"slack_email", email,
				"github_username", githubUsername,
				"override_email", overrideEmail)

			mapping := &ReverseMapping{
				CachedAt:       time.Now(),
				SlackUserID:    slackUserID,
				SlackUsername:  slackUser.Name,
				SlackEmail:     email,
				GitHubUsername: githubUsername,
				MatchMethod:    "config_override",
				Confidence:     100,
			}
			s.cacheMapping(mapping)
			return mapping, nil
		}
	}

	// Perform reverse lookup using org-wide cache
	githubUsername, confidence, matchMethod, err := s.reverseEmailLookup(ctx, email, organization)
	if err != nil {
		slog.Warn("reverse email lookup failed",
			"slack_user_id", slackUserID,
			"slack_email", email,
			"error", err)
		// Cache negative result
		mapping := &ReverseMapping{
			CachedAt:      time.Now(),
			SlackUserID:   slackUserID,
			SlackUsername: slackUser.Name,
			SlackEmail:    email,
			Confidence:    0,
		}
		s.cacheMapping(mapping)
		return nil, fmt.Errorf("no GitHub user found for email: %s", email)
	}

	mapping := &ReverseMapping{
		CachedAt:       time.Now(),
		SlackUserID:    slackUserID,
		SlackUsername:  slackUser.Name,
		SlackEmail:     email,
		GitHubUsername: githubUsername,
		MatchMethod:    matchMethod,
		Confidence:     confidence,
	}

	slog.Info("successfully mapped Slack user to GitHub",
		"slack_user_id", slackUserID,
		"slack_email", email,
		"github_username", githubUsername,
		"confidence", confidence,
		"match_method", matchMethod)

	s.cacheMapping(mapping)
	return mapping, nil
}

// reverseEmailLookup performs the actual reverse lookup: email → GitHub username.
// Uses the org-wide identity cache for fast, reliable lookups.
func (s *ReverseService) reverseEmailLookup(ctx context.Context, email, organization string) (username string, confidence int, matchMethod string, err error) {
	slog.Info("starting reverse email lookup via org cache",
		"email", email,
		"organization", organization)

	// Get org-wide identity cache
	cache, err := s.orgCache.OrgCache(ctx, organization)
	if err != nil {
		return "", 0, "", fmt.Errorf("failed to get org cache: %w", err)
	}

	// Look up GitHub username from email
	githubUsername, found := cache.LookupUsername(email)
	if !found {
		slog.Warn("email not found in org cache",
			"email", email,
			"organization", organization,
			"total_identities", len(cache.Identities))
		return "", 0, "", fmt.Errorf("email not found in org: %s", email)
	}

	// Find the identity to get confidence and source
	var identity *ghmailto.OrgIdentity
	for i := range cache.Identities {
		if cache.Identities[i].GitHubUsername == githubUsername {
			identity = &cache.Identities[i]
			break
		}
	}

	// Calculate confidence based on source
	confidence = 85 // Default confidence for org cache hit
	matchMethod = "org_cache"
	if identity != nil {
		if identity.Verified {
			confidence = 95
			matchMethod = "org_cache_verified"
		}
		slog.Info("found GitHub user via org cache",
			"email", email,
			"github_username", githubUsername,
			"source", identity.Source,
			"verified", identity.Verified,
			"confidence", confidence)
	}

	return githubUsername, confidence, matchMethod, nil
}

// cachedMapping retrieves a cached reverse mapping.
func (s *ReverseService) cachedMapping(slackUserID string) *ReverseMapping {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	if mapping, exists := s.cache[slackUserID]; exists {
		if time.Since(mapping.CachedAt) < cacheTTL {
			return mapping
		}
		// Expired, remove it lazily
	}

	return nil
}

// cacheMapping stores a reverse mapping in the cache.
func (s *ReverseService) cacheMapping(mapping *ReverseMapping) {
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
	s.cache[mapping.SlackUserID] = mapping
}

// ClearCache clears the reverse mapping cache.
func (s *ReverseService) ClearCache() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cache = make(map[string]*ReverseMapping)
	slog.Info("cleared reverse user mapping cache")
}

// CacheStats returns cache statistics for reverse mappings.
func (s *ReverseService) CacheStats() (total int, expired int) {
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
