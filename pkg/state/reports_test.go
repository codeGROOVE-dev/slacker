package state

import (
	"context"
	"testing"
	"time"
)

// TestMemoryStore_ReportSent tests LastReportSent and RecordReportSent
func TestMemoryStore_ReportSent(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	ctx := context.Background()

	// Test LastReportSent when nothing recorded
	lastSent, exists := store.LastReportSent(ctx, "U123")
	if exists {
		t.Error("LastReportSent(U123) exists = true, want false (nothing recorded yet)")
	}
	if !lastSent.IsZero() {
		t.Errorf("LastReportSent(U123) = %v, want zero time", lastSent)
	}

	// Record a report
	sentAt := time.Now()
	err := store.RecordReportSent(ctx, "U123", sentAt)
	if err != nil {
		t.Fatalf("RecordReportSent(U123) = %v, want nil", err)
	}

	// Verify it was recorded
	lastSent, exists = store.LastReportSent(ctx, "U123")
	if !exists {
		t.Error("LastReportSent(U123) exists = false, want true after recording")
	}
	if !lastSent.Equal(sentAt) {
		t.Errorf("LastReportSent(U123) = %v, want %v", lastSent, sentAt)
	}

	// Test different user
	_, exists = store.LastReportSent(ctx, "U456")
	if exists {
		t.Error("LastReportSent(U456) exists = true, want false (different user)")
	}

	// Record for different user
	sentAt2 := time.Now().Add(1 * time.Hour)
	err = store.RecordReportSent(ctx, "U456", sentAt2)
	if err != nil {
		t.Fatalf("RecordReportSent(U456) = %v, want nil", err)
	}

	lastSent2, exists := store.LastReportSent(ctx, "U456")
	if !exists {
		t.Error("LastReportSent(U456) exists = false, want true")
	}
	if !lastSent2.Equal(sentAt2) {
		t.Errorf("LastReportSent(U456) = %v, want %v", lastSent2, sentAt2)
	}

	// Verify first user still correct
	lastSent, exists = store.LastReportSent(ctx, "U123")
	if !exists {
		t.Error("LastReportSent(U123) exists = false, want true")
	}
	if !lastSent.Equal(sentAt) {
		t.Errorf("LastReportSent(U123) = %v, want %v (should be unchanged)", lastSent, sentAt)
	}
}

// TestJSONStore_ReportSent tests LastReportSent and RecordReportSent for JSON store
func TestJSONStore_ReportSent(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	store := &JSONStore{
		baseDir:       tempDir,
		threads:       make(map[string]ThreadInfo),
		dms:           make(map[string]time.Time),
		dmMessages:    make(map[string]DMInfo),
		digests:       make(map[string]time.Time),
		events:        make(map[string]time.Time),
		notifications: make(map[string]time.Time),
		reports:       make(map[string]time.Time),
	}

	ctx := context.Background()

	// Test LastReportSent when nothing recorded
	lastSent, exists := store.LastReportSent(ctx, "U789")
	if exists {
		t.Error("LastReportSent(U789) exists = true, want false")
	}
	if !lastSent.IsZero() {
		t.Errorf("LastReportSent(U789) = %v, want zero time", lastSent)
	}

	// Record a report
	sentAt := time.Now().Truncate(time.Second) // Truncate to avoid JSON precision issues
	err := store.RecordReportSent(ctx, "U789", sentAt)
	if err != nil {
		t.Fatalf("RecordReportSent(U789) = %v, want nil", err)
	}

	// Verify it was recorded
	lastSent, exists = store.LastReportSent(ctx, "U789")
	if !exists {
		t.Error("LastReportSent(U789) exists = false, want true after recording")
	}
	if !lastSent.Equal(sentAt) {
		t.Errorf("LastReportSent(U789) = %v, want %v", lastSent, sentAt)
	}
}

// TestDatastoreStore_ReportSent tests LastReportSent and RecordReportSent for Datastore (disabled mode)
func TestDatastoreStore_ReportSent(t *testing.T) {
	t.Parallel()

	// Create datastore store with disabled=true (uses memory only)
	store := &DatastoreStore{
		disabled: true,
		memory:   NewMemoryStore(),
	}

	ctx := context.Background()

	// Test LastReportSent when nothing recorded
	lastSent, exists := store.LastReportSent(ctx, "U000")
	if exists {
		t.Error("LastReportSent(U000) exists = true, want false")
	}

	// Record a report (should go to memory)
	sentAt := time.Now()
	err := store.RecordReportSent(ctx, "U000", sentAt)
	if err != nil {
		t.Fatalf("RecordReportSent(U000) = %v, want nil", err)
	}

	// Verify it was recorded in memory
	lastSent, exists = store.LastReportSent(ctx, "U000")
	if !exists {
		t.Error("LastReportSent(U000) exists = false, want true after recording")
	}
	if !lastSent.Equal(sentAt) {
		t.Errorf("LastReportSent(U000) = %v, want %v", lastSent, sentAt)
	}
}
