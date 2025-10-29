package usermapping

// Test helpers for integration testing.

// SetSlackClient sets the Slack client for testing.
// This is only exported for integration tests.
func (s *Service) SetSlackClient(client SlackAPI) {
	s.slackClient = client
}

// SetGitHubLookup sets the GitHub email lookup service for testing.
// This is only exported for integration tests.
func (s *Service) SetGitHubLookup(lookup GitHubEmailLookup) {
	s.githubLookup = lookup
}

// NewForTesting creates a new Service with injectable dependencies for testing.
func NewForTesting(slackClient SlackAPI, githubLookup GitHubEmailLookup) *Service {
	return &Service{
		slackClient:  slackClient,
		githubLookup: githubLookup,
		cache:        make(map[string]*UserMapping),
		lookupSem:    make(chan struct{}, maxConcurrentLookups),
	}
}
