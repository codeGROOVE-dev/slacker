package bot

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEventDeduplication verifies that the event deduplication logic works correctly.
func TestEventDeduplication(t *testing.T) {
	tests := []struct {
		name           string
		eventKey       string
		firstProcessed time.Time
		secondAttempt  time.Time
		wantDuplicate  bool
	}{
		{
			name:           "duplicate within 24 hours",
			eventKey:       "2025-10-19T10:00:00Z:https://github.com/org/repo/pull/1:pull_request",
			firstProcessed: time.Now().Add(-1 * time.Hour),
			secondAttempt:  time.Now(),
			wantDuplicate:  true,
		},
		{
			name:           "not duplicate after 24 hours",
			eventKey:       "2025-10-18T10:00:00Z:https://github.com/org/repo/pull/2:pull_request",
			firstProcessed: time.Now().Add(-25 * time.Hour),
			secondAttempt:  time.Now(),
			wantDuplicate:  false,
		},
		{
			name:           "first event not a duplicate",
			eventKey:       "2025-10-19T12:00:00Z:https://github.com/org/repo/pull/3:pull_request",
			firstProcessed: time.Time{}, // Zero time means not yet processed
			secondAttempt:  time.Now(),
			wantDuplicate:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate event deduplication logic
			processedEvents := make(map[string]time.Time)

			// First processing
			if !tt.firstProcessed.IsZero() {
				processedEvents[tt.eventKey] = tt.firstProcessed
			}

			// Cleanup old events (simulate the 24-hour cleanup logic)
			cutoff := time.Now().Add(-24 * time.Hour)
			for key, processedTime := range processedEvents {
				if processedTime.Before(cutoff) {
					delete(processedEvents, key)
				}
			}

			// Check if second attempt is duplicate
			_, exists := processedEvents[tt.eventKey]
			isDuplicate := exists

			if isDuplicate != tt.wantDuplicate {
				t.Errorf("deduplication check = %v, want %v", isDuplicate, tt.wantDuplicate)
			}
		})
	}
}

// TestSearchWindowOptimization verifies that we use PR creation date for search window.
func TestSearchWindowOptimization(t *testing.T) {
	tests := []struct {
		name           string
		prCreatedAt    time.Time
		expectedDays   int
		shouldFallback bool
	}{
		{
			name:           "recent PR uses creation date",
			prCreatedAt:    time.Now().Add(-3 * 24 * time.Hour), // 3 days ago
			expectedDays:   3,
			shouldFallback: false,
		},
		{
			name:           "old PR falls back to 30 days",
			prCreatedAt:    time.Now().Add(-45 * 24 * time.Hour), // 45 days ago
			expectedDays:   30,
			shouldFallback: true,
		},
		{
			name:           "missing creation date falls back",
			prCreatedAt:    time.Time{}, // Zero time
			expectedDays:   30,
			shouldFallback: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate search window logic
			searchFrom := tt.prCreatedAt
			usedFallback := false

			if searchFrom.IsZero() || time.Since(searchFrom) > 30*24*time.Hour {
				searchFrom = time.Now().AddDate(0, 0, -30)
				usedFallback = true
			}

			if usedFallback != tt.shouldFallback {
				t.Errorf("fallback usage = %v, want %v", usedFallback, tt.shouldFallback)
			}

			// Verify search window size
			actualDays := int(time.Since(searchFrom).Hours() / 24)
			tolerance := 1 // Allow 1 day tolerance for timing

			if actualDays < tt.expectedDays-tolerance || actualDays > tt.expectedDays+tolerance {
				t.Errorf("search window = %d days, want ~%d days", actualDays, tt.expectedDays)
			}
		})
	}
}

// TestDoubleCheckPreventsRace verifies the double-check logic conceptually.
// This is a unit test - integration test would require actual Slack API mocking.
func TestDoubleCheckPreventsRace(t *testing.T) {
	// Simulate scenario:
	// 1. Instance A searches → no thread
	// 2. Instance B searches → no thread
	// 3. Instance A double-checks → still no thread → creates thread
	// 4. Instance B double-checks → FINDS thread from A → uses it

	threadExists := false
	instanceAPassedFirstCheck := true
	instanceBPassedFirstCheck := true

	// Instance A proceeds to double-check and create
	if instanceAPassedFirstCheck {
		// Double-check (still no thread)
		if !threadExists {
			// Create thread
			threadExists = true
		}
	}

	// Instance B double-checks slightly later
	instanceBDoubleCheckFound := threadExists

	if !instanceBDoubleCheckFound {
		t.Error("Instance B should have found thread created by Instance A during double-check")
	}

	// Verify only one thread created
	threadCount := 0
	if instanceAPassedFirstCheck {
		threadCount++
	}
	if instanceBPassedFirstCheck && !instanceBDoubleCheckFound {
		threadCount++
	}

	if threadCount != 1 {
		t.Errorf("created %d threads, want 1", threadCount)
	}
}

// TestConcurrentEventDeduplication verifies that the processing lock prevents
// concurrent processing of the same event when sprinkler delivers duplicates.
func TestConcurrentEventDeduplication(t *testing.T) {
	// Simulate the exact scenario from the logs:
	// Sprinkler delivers the same event twice in quick succession (9ms apart)
	// Both should be detected and only one should process

	eventKey := "test-delivery-id-123"
	processedEvents := make(map[string]time.Time)
	processingEvents := make(map[string]bool)
	var processedMu sync.RWMutex
	var processingMu sync.Mutex

	var processedCount atomic.Int32
	var skippedCount atomic.Int32

	// Simulate two concurrent event deliveries
	var wg sync.WaitGroup
	wg.Add(2)

	// Event handler that mimics handleSprinklerEvent logic
	handleEvent := func(goroutineID int) {
		defer wg.Done()

		// Check if currently being processed (the new lock)
		processingMu.Lock()
		if processingEvents[eventKey] {
			processingMu.Unlock()
			t.Logf("Goroutine %d: Event already being processed, skipping", goroutineID)
			skippedCount.Add(1)
			return
		}
		// Mark as processing
		processingEvents[eventKey] = true
		processingMu.Unlock()

		// Cleanup on exit
		defer func() {
			processingMu.Lock()
			delete(processingEvents, eventKey)
			processingMu.Unlock()
		}()

		// Check in-memory processed events
		processedMu.Lock()
		if _, exists := processedEvents[eventKey]; exists {
			processedMu.Unlock()
			t.Logf("Goroutine %d: Event already processed (memory), skipping", goroutineID)
			skippedCount.Add(1)
			return
		}
		processedEvents[eventKey] = time.Now()
		processedMu.Unlock()

		// Simulate processing work (this would be processEvent in real code)
		t.Logf("Goroutine %d: Processing event", goroutineID)
		time.Sleep(10 * time.Millisecond) // Simulate work
		processedCount.Add(1)
	}

	// Start both goroutines nearly simultaneously (mimicking the 9ms gap in logs)
	go handleEvent(1)
	time.Sleep(1 * time.Millisecond) // Small delay to simulate the timing from logs
	go handleEvent(2)

	// Wait for both to complete
	wg.Wait()

	// Verify results
	processed := processedCount.Load()
	skipped := skippedCount.Load()

	if processed != 1 {
		t.Errorf("processed count = %d, want 1 (only one goroutine should process)", processed)
	}

	if skipped != 1 {
		t.Errorf("skipped count = %d, want 1 (second goroutine should skip)", skipped)
	}

	t.Logf("Final state: processed=%d, skipped=%d", processed, skipped)
}

// TestConcurrentEventDeduplicationStress is a stress test with many concurrent duplicates.
func TestConcurrentEventDeduplicationStress(t *testing.T) {
	const numConcurrentEvents = 100

	eventKey := "stress-test-event"
	processedEvents := make(map[string]time.Time)
	processingEvents := make(map[string]bool)
	var processedMu sync.RWMutex
	var processingMu sync.Mutex

	var processedCount atomic.Int32
	var skippedCount atomic.Int32

	var wg sync.WaitGroup
	wg.Add(numConcurrentEvents)

	handleEvent := func(_ int) {
		defer wg.Done()

		// Check if currently being processed
		processingMu.Lock()
		if processingEvents[eventKey] {
			processingMu.Unlock()
			skippedCount.Add(1)
			return
		}
		processingEvents[eventKey] = true
		processingMu.Unlock()

		defer func() {
			processingMu.Lock()
			delete(processingEvents, eventKey)
			processingMu.Unlock()
		}()

		// Check in-memory processed events
		processedMu.Lock()
		if _, exists := processedEvents[eventKey]; exists {
			processedMu.Unlock()
			skippedCount.Add(1)
			return
		}
		processedEvents[eventKey] = time.Now()
		processedMu.Unlock()

		// Simulate processing
		time.Sleep(1 * time.Millisecond)
		processedCount.Add(1)
	}

	// Launch all goroutines simultaneously
	for i := range numConcurrentEvents {
		go handleEvent(i)
	}

	wg.Wait()

	processed := processedCount.Load()
	skipped := skippedCount.Load()

	if processed != 1 {
		t.Errorf("processed count = %d, want 1 (only one should process)", processed)
	}

	if skipped != numConcurrentEvents-1 {
		t.Errorf("skipped count = %d, want %d", skipped, numConcurrentEvents-1)
	}

	t.Logf("Stress test: %d concurrent events, processed=%d, skipped=%d",
		numConcurrentEvents, processed, skipped)
}
