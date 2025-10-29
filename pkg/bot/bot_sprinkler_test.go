package bot

import (
	"testing"
	"time"

	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
)

func TestParsePRNumberFromURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		want      int
		wantError bool
	}{
		{
			name:      "valid URL",
			url:       "https://github.com/owner/repo/pull/123",
			want:      123,
			wantError: false,
		},
		{
			name:      "another valid URL",
			url:       "https://github.com/org/project/pull/456",
			want:      456,
			wantError: false,
		},
		{
			name:      "invalid URL - not github",
			url:       "https://example.com/owner/repo/pull/123",
			wantError: true,
		},
		{
			name:      "invalid URL - missing pull",
			url:       "https://github.com/owner/repo/issues/123",
			wantError: true,
		},
		{
			name:      "invalid URL - too few parts",
			url:       "https://github.com/owner",
			wantError: true,
		},
		{
			name:      "invalid URL - non-numeric PR",
			url:       "https://github.com/owner/repo/pull/abc",
			wantError: true,
		},
		{
			name:      "invalid URL - zero PR number",
			url:       "https://github.com/owner/repo/pull/0",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePRNumberFromURL(tt.url)
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("parsePRNumberFromURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEventKey(t *testing.T) {
	t.Run("with delivery_id", func(t *testing.T) {
		event := client.Event{
			Raw: map[string]interface{}{
				"delivery_id": "test-delivery-123",
			},
		}

		key := eventKey(event)
		if key != "test-delivery-123" {
			t.Errorf("expected key 'test-delivery-123', got %s", key)
		}
	})

	t.Run("without delivery_id", func(t *testing.T) {
		timestamp := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
		event := client.Event{
			Timestamp: timestamp,
			URL:       "https://github.com/owner/repo/pull/123",
			Type:      "pull_request",
			Raw:       map[string]interface{}{},
		}

		key := eventKey(event)
		expectedKey := timestamp.Format(time.RFC3339Nano) + ":https://github.com/owner/repo/pull/123:pull_request"
		if key != expectedKey {
			t.Errorf("expected key %s, got %s", expectedKey, key)
		}
	})

	t.Run("nil raw map", func(t *testing.T) {
		timestamp := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
		event := client.Event{
			Timestamp: timestamp,
			URL:       "https://github.com/owner/repo/pull/456",
			Type:      "check_run",
			Raw:       nil,
		}

		key := eventKey(event)
		expectedKey := timestamp.Format(time.RFC3339Nano) + ":https://github.com/owner/repo/pull/456:check_run"
		if key != expectedKey {
			t.Errorf("expected key %s, got %s", expectedKey, key)
		}
	})

	t.Run("empty delivery_id falls back to timestamp", func(t *testing.T) {
		timestamp := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
		event := client.Event{
			Timestamp: timestamp,
			URL:       "https://github.com/owner/repo/pull/789",
			Type:      "pull_request_review",
			Raw: map[string]interface{}{
				"delivery_id": "",
			},
		}

		key := eventKey(event)
		expectedKey := timestamp.Format(time.RFC3339Nano) + ":https://github.com/owner/repo/pull/789:pull_request_review"
		if key != expectedKey {
			t.Errorf("expected key %s, got %s", expectedKey, key)
		}
	})
}
