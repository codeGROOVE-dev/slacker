// Package bot implements the coordination logic between GitHub, Slack, and notifications.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/slacker/pkg/config"
	"github.com/codeGROOVE-dev/slacker/pkg/github"
	"github.com/codeGROOVE-dev/slacker/pkg/notify"
	"github.com/codeGROOVE-dev/slacker/pkg/slack"
	"github.com/codeGROOVE-dev/slacker/pkg/state"
)

// Coordinator coordinates between GitHub, Slack, and notifications.
type Coordinator struct {
	slack         *slack.Client
	github        *github.Client
	stateManager  *state.Manager
	configManager *config.Manager
	notifier      *notify.Manager
	sprinklerURL  string
}

// New creates a new bot coordinator.
func New(
	ctx context.Context,
	slackClient *slack.Client,
	githubClient *github.Client,
	stateManager *state.Manager,
	configManager *config.Manager,
	notifier *notify.Manager,
	sprinklerURL string,
) *Coordinator {
	c := &Coordinator{
		slack:         slackClient,
		github:        githubClient,
		stateManager:  stateManager,
		configManager: configManager,
		notifier:      notifier,
		sprinklerURL:  sprinklerURL,
	}

	// Set GitHub client in config manager.
	configManager.SetGitHubClient(githubClient.Client())

	// Get workspace info and set in config manager for validation.
	if teamInfo, err := slackClient.GetWorkspaceInfo(ctx); err == nil {
		// Use the team domain as workspace identifier
		workspaceName := teamInfo.Domain + ".slack.com"
		configManager.SetWorkspaceName(workspaceName)
		slog.Info("set workspace name for config validation", "workspace", workspaceName)
	} else {
		slog.Warn("failed to get workspace info, config validation disabled", "error", err)
	}

	return c
}

// SprinklerMessage represents a message from sprinkler.
type SprinklerMessage struct {
	Type    string          `json:"type,omitempty"`    // Message type (e.g., "ping", "event")
	Event   string          `json:"event,omitempty"`   // GitHub event type
	Repo    string          `json:"repo,omitempty"`    // Repository name
	Payload json.RawMessage `json:"payload,omitempty"` // Event payload
}

// processEvent processes a GitHub webhook event.
func (c *Coordinator) processEvent(ctx context.Context, msg SprinklerMessage) error {
	// Skip empty messages (likely subscription confirmations or keepalives)
	if msg.Event == "" && msg.Repo == "" {
		slog.Debug("received empty message from sprinkler, likely acknowledgment")
		return nil
	}

	// Skip messages without repo information
	if msg.Repo == "" {
		slog.Debug("received message without repo", "event", msg.Event)
		return nil
	}

	slog.Info("processing event", "event", msg.Event, "repo", msg.Repo)

	// Parse repo owner and name.
	parts := strings.Split(msg.Repo, "/")
	if len(parts) != 2 {
		slog.Warn("invalid repo format", "repo", msg.Repo)
		return fmt.Errorf("invalid repo format: %s", msg.Repo)
	}
	owner := parts[0]
	repo := parts[1]

	if owner == "" || repo == "" {
		slog.Warn("empty owner or repo name", "owner", owner, "repo", repo)
		return errors.New("empty owner or repo name")
	}

	// Load config for this org if not already loaded.
	if _, exists := c.configManager.Config(owner); !exists {
		if err := c.configManager.LoadConfig(ctx, owner); err != nil {
			slog.Warn("failed to load config for org", "org", owner, "error", err)
		}
	}

	// Handle different event types.
	switch msg.Event {
	case "pull_request":
		c.handlePullRequestEvent(ctx, owner, repo, msg.Payload)
	case "pull_request_review":
		c.handlePullRequestReviewEvent(ctx, owner, repo, msg.Payload)
	case "check_run", "check_suite":
		// Parse to get PR number.
		// This is simplified - in production, we'd need to map commits to PRs.
		slog.Debug("received check event", "owner", owner, "repo", repo)
	case "push":
		// Check if this is a push to .codeGROOVE repo.
		if repo == ".codeGROOVE" {
			slog.Info("reloading config", "org", owner)
			if err := c.configManager.ReloadConfig(ctx, owner); err != nil {
				slog.Warn("failed to reload config", "error", err)
			}
		}
	default:
		slog.Debug("unhandled event type", "event", msg.Event)
	}

	return nil
}

// handlePullRequestEvent handles pull request events.
func (c *Coordinator) handlePullRequestEvent(ctx context.Context, owner, repo string, payload json.RawMessage) {
	var event struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			User   struct {
				Login string `json:"login"`
			} `json:"user"`
			HTMLURL string `json:"html_url"`
		} `json:"pull_request"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		slog.Warn("failed to unmarshal PR event", "error", err)
		return
	}

	slog.Info("PR event", "owner", owner, "repo", repo, "number", event.Number, "action", event.Action)

	// Get channels for this repo.
	channels := c.configManager.ChannelsForRepo(owner, repo)
	slog.Info("evaluating PR for channel notifications",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
		"action", event.Action,
		"title", event.PullRequest.Title,
		"author", event.PullRequest.User.Login,
		"configured_channels", len(channels),
		"channels", channels)

	if len(channels) == 0 {
		slog.Info("no channels configured for PR - skipping channel notifications",
			"owner", owner,
			"repo", repo,
			"pr_number", event.Number)
		return
	}

	// Get PR state.
	prState, blockedOn, err := c.github.PRState(ctx, owner, repo, event.Number)
	if err != nil {
		slog.Error("failed to get PR state - cannot process notifications",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
			"error", err)
		return
	}

	slog.Info("retrieved PR state for notification processing",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
		"state", prState,
		"blocked_on_users", len(blockedOn),
		"blocked_on", blockedOn)

	// For now, use a default workspace ID.
	// In production, this would map channels to workspaces.
	workspaceID := "default"

	// Update or create PR state.
	pr := &state.PRState{
		Owner:       owner,
		Repo:        repo,
		Number:      event.Number,
		Title:       event.PullRequest.Title,
		Author:      event.PullRequest.User.Login,
		State:       prState,
		BlockedOn:   blockedOn,
		LastUpdated: time.Now(),
	}

	// Check if we already have a thread for this PR.
	existingPR, exists := c.stateManager.PRState(workspaceID, owner, repo, event.Number)
	if exists {
		pr.ThreadTS = existingPR.ThreadTS
		pr.ChannelID = existingPR.ChannelID
	}

	// Handle based on action.
	slog.Info("processing PR action",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
		"action", event.Action,
		"will_process", true)

	switch event.Action {
	case "opened", "reopened":
		slog.Info("creating channel notifications for new/reopened PR",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
			"channels_to_notify", len(channels),
			"channels", channels)

		// Create threads in configured channels.
		for _, channel := range channels {
			if pr.ThreadTS != "" {
				slog.Debug("PR already has thread, skipping channel",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
					"channel", channel,
					"existing_thread", pr.ThreadTS)
				continue
			}

			slog.Info("creating thread in channel",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
				"channel", channel,
				"pr_title", event.PullRequest.Title,
				"pr_author", event.PullRequest.User.Login)

			// Create new thread.
			threadTS, err := c.createPRThread(ctx, channel, owner, repo, event.Number, event.PullRequest)
			if err != nil {
				slog.Error("failed to create thread in channel",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
					"channel", channel,
					"error", err,
					"will_try_next_channel", true)
				continue
			}
			pr.ThreadTS = threadTS
			pr.ChannelID = channel

			// Track that we notified users in this channel
			c.stateManager.UpdateChannelNotification(workspaceID, owner, repo, event.Number)

			slog.Info("successfully created channel notification thread",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
				"channel", channel,
				"thread_ts", threadTS,
				"channel_notification_tracked", true,
				"users_will_be_delayed_for_dms", true)
		}

	case "closed":
		slog.Info("updating PR state for closed PR",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
			"has_thread", pr.ThreadTS != "",
			"channel", pr.ChannelID)

		// Update state in existing thread.
		if pr.ThreadTS != "" {
			if err := c.slack.UpdateReactions(ctx, pr.ChannelID, pr.ThreadTS, prState); err != nil {
				slog.Error("failed to update reaction for closed PR",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
					"channel", pr.ChannelID,
					"thread_ts", pr.ThreadTS,
					"error", err)
			} else {
				slog.Info("updated channel thread for closed PR",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
					"channel", pr.ChannelID,
					"new_state", prState)
			}
		}

	case "synchronize", "edited":
		slog.Info("updating PR state for synchronized/edited PR",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
			"has_thread", pr.ThreadTS != "",
			"channel", pr.ChannelID,
			"new_state", prState)

		// Update state.
		if pr.ThreadTS != "" {
			if err := c.slack.UpdateReactions(ctx, pr.ChannelID, pr.ThreadTS, prState); err != nil {
				slog.Error("failed to update reaction for synchronized/edited PR",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
					"channel", pr.ChannelID,
					"thread_ts", pr.ThreadTS,
					"error", err)
			} else {
				slog.Info("updated channel thread for synchronized/edited PR",
					"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
					"channel", pr.ChannelID,
					"new_state", prState)
			}
		}
	default:
		slog.Info("unhandled PR action - no processing required",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
			"action", event.Action,
			"ignored", true)
	}

	// Save PR state.
	c.stateManager.SetPRState(workspaceID, pr)

	slog.Info("PR state updated and saved",
		"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
		"workspace", workspaceID,
		"final_state", prState,
		"thread_ts", pr.ThreadTS,
		"channel", pr.ChannelID)

	// Check if we need to notify blocked users.
	if len(blockedOn) > 0 {
		slog.Info("processing blocked users for PR notifications",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
			"blocked_users_count", len(blockedOn),
			"blocked_users", blockedOn,
			"pr_state", prState)

		for _, userID := range blockedOn {
			// In production, map GitHub username to Slack user ID.
			// Then update their app home view.
			slog.Info("user is blocking PR - potential notification candidate",
				"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
				"github_user", userID,
				"pr_state", prState,
				"pr_author", event.PullRequest.User.Login,
				"needs_slack_mapping", true)
			// Would call: c.updateUserHome(ctx, workspaceID, slackUserID)
			// Would also call: c.notifier.NotifyUser(ctx, workspaceID, slackUserID, pr.ChannelID, pr)
		}
	} else {
		slog.Info("no users blocking PR - no notifications needed",
			"pr", fmt.Sprintf("%s/%s#%d", owner, repo, event.Number),
			"pr_state", prState)
	}
}

// handlePullRequestReviewEvent handles PR review events.
func (c *Coordinator) handlePullRequestReviewEvent(ctx context.Context, owner, repo string, payload json.RawMessage) {
	var event struct {
		Action string `json:"action"`
		Review struct {
			User struct {
				Login string `json:"login"`
			} `json:"user"`
			State string `json:"state"`
		} `json:"review"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}

	if err := json.Unmarshal(payload, &event); err != nil {
		slog.Warn("failed to unmarshal review event", "error", err)
		return
	}

	workspaceID := "default"
	pr, exists := c.stateManager.PRState(workspaceID, owner, repo, event.PullRequest.Number)
	if !exists {
		return
	}

	// Update thread with review status.
	if pr.ThreadTS != "" && event.Action == "submitted" {
		message := fmt.Sprintf("@%s reviewed the PR", event.Review.User.Login)
		switch event.Review.State {
		case "approved":
			message += " ✅"
		case "changes_requested":
			message += " 🔧"
		default:
			// Other review states (commented, dismissed, etc.)
			message += fmt.Sprintf(" (%s)", event.Review.State)
		}
		if err := c.slack.PostThreadReply(ctx, pr.ChannelID, pr.ThreadTS, message); err != nil {
			slog.Warn("failed to send thread update", "error", err)
		}
	}

	// Update PR state.
	prState, blockedOn, err := c.github.PRState(ctx, owner, repo, event.PullRequest.Number)
	if err == nil {
		pr.State = prState
		pr.BlockedOn = blockedOn
		pr.LastUpdated = time.Now()
		c.stateManager.SetPRState(workspaceID, pr)

		// Update reaction.
		if pr.ThreadTS != "" {
			if err := c.slack.UpdateReactions(ctx, pr.ChannelID, pr.ThreadTS, prState); err != nil {
				slog.Warn("failed to update reaction", "error", err)
			}
		}
	}
}

// createPRThread creates a new thread in Slack for a PR.
func (c *Coordinator) createPRThread(ctx context.Context, channel, owner, repo string, number int, pr struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL string `json:"html_url"`
},
) (string, error) {
	// Get prefix for this org.
	prefix := c.configManager.Prefix(owner)

	// Format message.
	text := fmt.Sprintf(
		"%s %s • <%s|%s/%s#%d> by @%s",
		prefix,
		pr.Title,
		pr.HTMLURL,
		owner,
		repo,
		number,
		pr.User.Login,
	)

	// Create thread.
	threadTS, err := c.slack.PostThread(ctx, channel, text, nil)
	if err != nil {
		return "", fmt.Errorf("failed to post thread: %w", err)
	}

	// Add initial reaction based on state.
	prState, _, err := c.github.PRState(ctx, owner, repo, number)
	if err == nil {
		if err := c.slack.UpdateReactions(ctx, channel, threadTS, prState); err != nil {
			slog.Warn("failed to add initial reaction", "error", err)
		}
	}

	return threadTS, nil
}
