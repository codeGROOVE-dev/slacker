package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/bot/cache"
)

func TestProcessEvent_EmptyMessage(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		configManager: NewMockConfig().Build(),
	}

	msg := SprinklerMessage{
		Event: "",
		Repo:  "",
	}

	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error for empty message, got: %v", err)
	}
}

func TestProcessEvent_NoRepo(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		configManager: NewMockConfig().Build(),
	}

	msg := SprinklerMessage{
		Event: "pull_request",
		Repo:  "",
	}

	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error for message without repo, got: %v", err)
	}
}

func TestProcessEvent_InvalidRepoFormat(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		configManager: NewMockConfig().Build(),
	}

	tests := []struct {
		name string
		repo string
	}{
		{
			name: "single part",
			repo: "invalidrepo",
		},
		{
			name: "three parts",
			repo: "owner/repo/extra",
		},
		{
			name: "empty owner",
			repo: "/repo",
		},
		{
			name: "empty repo",
			repo: "owner/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := SprinklerMessage{
				Event: "pull_request",
				Repo:  tt.repo,
			}

			err := c.processEvent(ctx, msg)
			if err == nil {
				t.Error("expected error for invalid repo format, got nil")
			}
		})
	}
}

func TestProcessEvent_UnhandledEventType(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		configManager: NewMockConfig().Build(),
	}

	msg := SprinklerMessage{
		Event: "unknown_event",
		Repo:  "testorg/testrepo",
	}

	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error for unhandled event type, got: %v", err)
	}
}

func TestProcessEvent_PushToCodeGROOVERepo(t *testing.T) {
	ctx := context.Background()

	cfg := NewMockConfig().Build()
	c := &Coordinator{
		configManager: cfg,
	}

	msg := SprinklerMessage{
		Event:     "push",
		Repo:      "testorg/.codeGROOVE",
		Timestamp: time.Now(),
	}

	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error for push to .codeGROOVE, got: %v", err)
	}
}

func TestProcessEvent_CheckEventWithoutPR(t *testing.T) {
	ctx := context.Background()

	c := &Coordinator{
		configManager: NewMockConfig().Build(),
	}

	tests := []struct {
		name  string
		event string
	}{
		{
			name:  "check_run without PR",
			event: "check_run",
		},
		{
			name:  "check_suite without PR",
			event: "check_suite",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := SprinklerMessage{
				Event:    tt.event,
				Repo:     "testorg/testrepo",
				PRNumber: 0, // No PR number
				URL:      "https://github.com/testorg/testrepo",
			}

			err := c.processEvent(ctx, msg)
			if err != nil {
				t.Errorf("expected nil error for check event without PR, got: %v", err)
			}
		})
	}
}

func TestProcessEvent_CheckEventWithPR(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "", // No token - will return early
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	tests := []struct {
		name  string
		event string
	}{
		{
			name:  "check_run with PR",
			event: "check_run",
		},
		{
			name:  "check_suite with PR",
			event: "check_suite",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := SprinklerMessage{
				Event:    tt.event,
				Repo:     "testorg/testrepo",
				PRNumber: 42,
				URL:      "https://github.com/testorg/testrepo/pull/42",
			}

			err := c.processEvent(ctx, msg)
			if err != nil {
				t.Errorf("expected nil error for check event with PR, got: %v", err)
			}
		})
	}
}

func TestProcessEvent_PullRequestReview(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "", // No token - will return early
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	msg := SprinklerMessage{
		Event:    "pull_request_review",
		Repo:     "testorg/testrepo",
		PRNumber: 42,
		URL:      "https://github.com/testorg/testrepo/pull/42",
	}

	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error for pull_request_review event, got: %v", err)
	}
}

func TestProcessEvent_PullRequest(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "", // No token - will return early
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	msg := SprinklerMessage{
		Event:     "pull_request",
		Repo:      "testorg/testrepo",
		PRNumber:  42,
		URL:       "https://github.com/testorg/testrepo/pull/42",
		Timestamp: time.Now(),
	}

	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error for pull_request event, got: %v", err)
	}
}

func TestProcessEvent_PullRequestCodeGROOVE(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  NewMockConfig().Build(),
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	msg := SprinklerMessage{
		Event:     "pull_request",
		Repo:      "testorg/.codeGROOVE",
		PRNumber:  42,
		URL:       "https://github.com/testorg/.codeGROOVE/pull/42",
		Timestamp: time.Now(),
	}

	// Should handle .codeGROOVE PR specially (logs about cache invalidation)
	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error for .codeGROOVE PR event, got: %v", err)
	}
}

// TestProcessEvent_ConfigLoadError tests error handling when config loading fails
func TestProcessEvent_ConfigLoadError(t *testing.T) {
	ctx := context.Background()

	mockGH := &mockGitHub{
		org:   "testorg",
		token: "test-token",
	}

	// MockConfig with no loaded configs - will trigger LoadConfig which will fail
	mockConfig := NewMockConfig().
		WithLoadError(errors.New("config load failed")).
		Build()

	c := &Coordinator{
		github:         mockGH,
		slack:          &mockSlackClient{},
		stateStore:     &mockStateStore{processedEvents: make(map[string]bool)},
		configManager:  mockConfig,
		threadCache:    cache.New(),
		eventSemaphore: make(chan struct{}, 10),
	}

	msg := SprinklerMessage{
		Event:     "pull_request",
		Repo:      "neworg/repo",
		PRNumber:  1,
		URL:       "https://github.com/neworg/repo/pull/1",
		Timestamp: time.Now(),
	}

	// Should handle config load error gracefully (log warning but continue)
	err := c.processEvent(ctx, msg)
	// No error should be returned even if config loading fails
	if err != nil {
		t.Errorf("expected nil error even with config load failure, got: %v", err)
	}
}

// TestProcessEvent_PushToCodeGROOVEConfigReloadError tests reload error handling
func TestProcessEvent_PushToCodeGROOVEConfigReloadError(t *testing.T) {
	ctx := context.Background()

	// MockConfig with error injection for ReloadConfig
	mockConfig := NewMockConfig().
		WithReloadConfigError().
		WithOrg("testorg").
		Build()

	c := &Coordinator{
		github:        &mockGitHub{org: "testorg", token: "test-token"},
		slack:         &mockSlackClient{},
		stateStore:    &mockStateStore{processedEvents: make(map[string]bool)},
		configManager: mockConfig,
		threadCache:   cache.New(),
	}

	msg := SprinklerMessage{
		Event:     "push",
		Repo:      "testorg/.codeGROOVE",
		Timestamp: time.Now(),
	}

	// Should handle reload error gracefully (log warning but continue)
	err := c.processEvent(ctx, msg)
	if err != nil {
		t.Errorf("expected nil error even with config reload failure, got: %v", err)
	}
}
