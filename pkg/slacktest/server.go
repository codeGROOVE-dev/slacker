// Package slacktest provides a mock Slack HTTP server for integration testing.
package slacktest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// Server represents a mock Slack API server.
//
//nolint:govet // fieldalignment optimization would reduce test clarity
type Server struct {
	*httptest.Server

	mu sync.RWMutex

	// User lookup configuration
	usersByEmail map[string]*slack.User

	// Channel configuration
	channels        map[string]*Channel
	channelMembers  map[string][]string // channelID -> []userID
	botInChannels   map[string]bool     // channelID -> true if bot is member
	channelMessages map[string][]*Message

	// DM tracking
	dmChannels map[string]string // userID -> dmChannelID

	// Request tracking for assertions
	PostedMessages  []*PostedMessage
	UpdatedMessages []*UpdatedMessage
	EmailLookups    []string
}

// Channel represents a Slack channel.
type Channel struct {
	ID       string
	Name     string
	IsMember bool
}

// Message represents a Slack message.
type Message struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	User      string `json:"user"`
	Timestamp string `json:"ts"`
}

// PostedMessage tracks a message posted via chat.postMessage.
type PostedMessage struct {
	Channel   string
	Text      string
	Timestamp time.Time
	ThreadTS  string
}

// UpdatedMessage tracks a message updated via chat.update.
//
//nolint:govet // fieldalignment optimization not worth the complexity
type UpdatedMessage struct {
	Channel   string
	Timestamp string
	Text      string
	UpdatedAt time.Time
}

// New creates and starts a new mock Slack server.
func New() *Server {
	s := &Server{
		usersByEmail:    make(map[string]*slack.User),
		channels:        make(map[string]*Channel),
		channelMembers:  make(map[string][]string),
		botInChannels:   make(map[string]bool),
		channelMessages: make(map[string][]*Message),
		dmChannels:      make(map[string]string),
		PostedMessages:  make([]*PostedMessage, 0),
		UpdatedMessages: make([]*UpdatedMessage, 0),
		EmailLookups:    make([]string, 0),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/users.lookupByEmail", s.handleUserLookupByEmail)
	mux.HandleFunc("/api/conversations.info", s.handleConversationsInfo)
	mux.HandleFunc("/api/conversations.members", s.handleConversationsMembers)
	mux.HandleFunc("/api/chat.postMessage", s.handleChatPostMessage)
	mux.HandleFunc("/api/chat.update", s.handleChatUpdate)
	mux.HandleFunc("/api/conversations.history", s.handleConversationsHistory)
	mux.HandleFunc("/api/conversations.open", s.handleConversationsOpen)
	mux.HandleFunc("/api/users.info", s.handleUsersInfo)
	mux.HandleFunc("/api/users.getPresence", s.handleUsersGetPresence)
	mux.HandleFunc("/api/auth.test", s.handleAuthTest)

	s.Server = httptest.NewServer(mux)
	return s
}

// AddUser adds a user that can be looked up by email.
func (s *Server) AddUser(email, userID, username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usersByEmail[email] = &slack.User{
		ID:   userID,
		Name: username,
		Profile: slack.UserProfile{
			Email: email,
		},
	}
}

// AddChannel adds a channel to the mock server.
func (s *Server) AddChannel(channelID, channelName string, botIsMember bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels[channelID] = &Channel{
		ID:       channelID,
		Name:     channelName,
		IsMember: botIsMember,
	}
	s.botInChannels[channelID] = botIsMember
}

// AddChannelMember adds a user to a channel's member list.
func (s *Server) AddChannelMember(channelID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelMembers[channelID] = append(s.channelMembers[channelID], userID)
}

// AddMessage adds a message to a channel's history.
func (s *Server) AddMessage(channelID, text, timestamp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelMessages[channelID] = append(s.channelMessages[channelID], &Message{
		Type:      "message",
		Text:      text,
		Timestamp: timestamp,
	})
}

// GetPostedMessages returns all messages posted via chat.postMessage.
func (s *Server) GetPostedMessages() []*PostedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.PostedMessages
}

// GetUpdatedMessages returns all messages updated via chat.update.
func (s *Server) GetUpdatedMessages() []*UpdatedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.UpdatedMessages
}

// GetEmailLookups returns all emails that were looked up.
func (s *Server) GetEmailLookups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.EmailLookups
}

// Reset clears all tracking data (but keeps configuration).
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PostedMessages = make([]*PostedMessage, 0)
	s.UpdatedMessages = make([]*UpdatedMessage, 0)
	s.EmailLookups = make([]string, 0)
}

func (s *Server) handleUserLookupByEmail(w http.ResponseWriter, r *http.Request) {
	// Slack API uses GET with query params or POST with form data
	var email string
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			email = r.FormValue("email")
		}
	} else {
		email = r.URL.Query().Get("email")
	}

	s.mu.Lock()
	s.EmailLookups = append(s.EmailLookups, email)
	user, exists := s.usersByEmail[email]
	s.mu.Unlock()

	if !exists {
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "users_not_found",
		})
		return
	}

	s.writeJSON(w, map[string]any{
		"ok":   true,
		"user": user,
	})
}

func (s *Server) handleConversationsInfo(w http.ResponseWriter, r *http.Request) {
	// Slack API uses GET with query params or POST with form data
	var channelID string
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			channelID = r.FormValue("channel")
		}
	} else {
		channelID = r.URL.Query().Get("channel")
	}

	s.mu.RLock()
	channel, exists := s.channels[channelID]
	s.mu.RUnlock()

	if !exists {
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "channel_not_found",
		})
		return
	}

	s.writeJSON(w, map[string]any{
		"ok": true,
		"channel": map[string]any{
			"id":         channel.ID,
			"name":       channel.Name,
			"is_member":  channel.IsMember,
			"is_channel": true,
		},
	})
}

func (s *Server) handleConversationsMembers(w http.ResponseWriter, r *http.Request) {
	// Slack API uses GET with query params or POST with form data
	var channelID string
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			channelID = r.FormValue("channel")
		}
	} else {
		channelID = r.URL.Query().Get("channel")
	}

	s.mu.RLock()
	members, exists := s.channelMembers[channelID]
	s.mu.RUnlock()

	if !exists {
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "channel_not_found",
		})
		return
	}

	s.writeJSON(w, map[string]any{
		"ok":      true,
		"members": members,
	})
}

func (s *Server) handleChatPostMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "invalid_form",
		})
		return
	}

	channel := r.FormValue("channel")
	text := r.FormValue("text")
	threadTS := r.FormValue("thread_ts")

	timestamp := time.Now().Format("1504898400.123456")

	s.mu.Lock()
	s.PostedMessages = append(s.PostedMessages, &PostedMessage{
		Channel:   channel,
		Text:      text,
		Timestamp: time.Now(),
		ThreadTS:  threadTS,
	})
	s.mu.Unlock()

	s.writeJSON(w, map[string]any{
		"ok":      true,
		"channel": channel,
		"ts":      timestamp,
		"message": map[string]any{
			"text": text,
			"type": "message",
		},
	})
}

func (s *Server) handleChatUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "invalid_form",
		})
		return
	}

	channel := r.FormValue("channel")
	text := r.FormValue("text")
	ts := r.FormValue("ts")

	s.mu.Lock()
	s.UpdatedMessages = append(s.UpdatedMessages, &UpdatedMessage{
		Channel:   channel,
		Timestamp: ts,
		Text:      text,
		UpdatedAt: time.Now(),
	})
	s.mu.Unlock()

	s.writeJSON(w, map[string]any{
		"ok":      true,
		"channel": channel,
		"ts":      ts,
		"text":    text,
	})
}

func (s *Server) handleConversationsHistory(w http.ResponseWriter, r *http.Request) {
	// Slack API uses GET with query params or POST with form data
	var channelID string
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			channelID = r.FormValue("channel")
		}
	} else {
		channelID = r.URL.Query().Get("channel")
	}

	s.mu.RLock()
	messages, exists := s.channelMessages[channelID]
	s.mu.RUnlock()

	if !exists {
		s.writeJSON(w, map[string]any{
			"ok":       true,
			"messages": []any{},
		})
		return
	}

	s.writeJSON(w, map[string]any{
		"ok":       true,
		"messages": messages,
	})
}

func (s *Server) handleConversationsOpen(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.writeJSON(w, map[string]any{
			"ok":    false,
			"error": "invalid_form",
		})
		return
	}

	users := r.FormValue("users")
	userID := strings.TrimSpace(users)

	s.mu.Lock()
	dmChannelID, exists := s.dmChannels[userID]
	if !exists {
		dmChannelID = "D" + userID[1:] // Convert U123 -> D123
		s.dmChannels[userID] = dmChannelID
	}
	s.mu.Unlock()

	s.writeJSON(w, map[string]any{
		"ok": true,
		"channel": map[string]any{
			"id": dmChannelID,
		},
	})
}

func (s *Server) handleUsersInfo(w http.ResponseWriter, r *http.Request) {
	// Slack API uses GET with query params or POST with form data
	var userID string
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			userID = r.FormValue("user")
		}
	} else {
		userID = r.URL.Query().Get("user")
	}

	// For testing purposes, all users are considered active
	s.writeJSON(w, map[string]any{
		"ok": true,
		"user": map[string]any{
			"id": userID,
			"presence": map[string]any{
				"presence": "active",
			},
		},
	})
}

func (s *Server) handleUsersGetPresence(w http.ResponseWriter, _ *http.Request) {
	// For testing purposes, all users are considered active
	s.writeJSON(w, map[string]any{
		"ok":       true,
		"presence": "active",
		"online":   true,
	})
}

func (s *Server) handleAuthTest(w http.ResponseWriter, _ *http.Request) {
	// Return mock bot info for testing
	s.writeJSON(w, map[string]any{
		"ok":      true,
		"url":     "https://test-workspace.slack.com/",
		"team":    "Test Workspace",
		"user":    "test-bot",
		"team_id": "T123456",
		"user_id": "U123BOT",
		"bot_id":  "B123BOT",
	})
}

func (*Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
