package state

import (
	"testing"
)

func TestThreadKey(t *testing.T) {
	key := threadKey("owner", "repo", 123, "C123")
	expected := "owner/repo#123:C123"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestDMKey(t *testing.T) {
	key := dmKey("U001", "https://github.com/owner/repo/pull/123")
	expected := "dm:U001:https://github.com/owner/repo/pull/123"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestDigestKey(t *testing.T) {
	key := digestKey("U001", "2025-01-15")
	expected := "digest:U001:2025-01-15"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}
