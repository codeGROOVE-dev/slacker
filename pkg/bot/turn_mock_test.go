package bot

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockTurnServer creates an httptest server that mocks turnclient API responses.
func mockTurnServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a simple JSON response that matches turnclient's CheckResponse structure
		// This is a minimal valid response for testing purposes
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"timestamp": "2025-11-05T00:00:00Z",
			"commit": "abc123",
			"pull_request": {
				"title": "Test PR",
				"author": "testauthor",
				"state": "open",
				"merged": false,
				"draft": false,
				"created_at": "2025-11-04T00:00:00Z",
				"updated_at": "2025-11-05T00:00:00Z",
				"url": "https://github.com/testorg/testrepo/pull/42",
				"commits": []
			},
			"analysis": {
				"workflow_state": "APPROVED_WAITING_FOR_MERGE",
				"next_action": {},
				"state_transitions": [],
				"ready_to_merge": true,
				"approved": true,
				"changes_requested": false,
				"review_required": false,
				"unresolved_comments": 0,
				"blocking_reasons": []
			}
		}`))
	}))
}
