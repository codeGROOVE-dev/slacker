package bot

import (
	"testing"
	"time"
)

func TestCoordinator_saveThread(t *testing.T) {
	// Create mock state store
	mockStore := &mockStateStore{
		threads: make(map[string]ThreadInfo),
	}

	// Create coordinator with mock
	c := &Coordinator{
		stateStore: mockStore,
		threadCache: &ThreadCache{
			prThreads: make(map[string]ThreadInfo),
			creating:  make(map[string]bool),
		},
	}

	// Test saving thread
	owner := "testorg"
	repo := "testrepo"
	number := 123
	channelID := "C123"
	info := ThreadInfo{
		ThreadTS:      "1234567890.123456",
		MessageText:   "Test PR message",
		ChannelID:     channelID,
		LastState:     "awaiting_review",
		UpdatedAt:     time.Now(),
		LastEventTime: time.Now(),
	}

	c.saveThread(owner, repo, number, channelID, info)

	// Verify thread was saved to cache
	cacheKey := owner + "/" + repo + "#123:" + channelID
	cachedInfo, exists := c.threadCache.Get(cacheKey)
	if !exists {
		t.Error("expected thread to be saved in cache")
	}
	if cachedInfo.ThreadTS != info.ThreadTS {
		t.Errorf("expected ThreadTS %s, got %s", info.ThreadTS, cachedInfo.ThreadTS)
	}
	if cachedInfo.MessageText != info.MessageText {
		t.Errorf("expected MessageText %s, got %s", info.MessageText, cachedInfo.MessageText)
	}

	// Note: We can't easily verify state store save without more complex setup
	// since it happens in a goroutine
}
