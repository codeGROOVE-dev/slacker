package slack

import (
	"errors"
	"testing"
)

func TestIsRateLimitError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "rate_limited error",
			err:  errors.New("slack API error: rate_limited"),
			want: true,
		},
		{
			name: "429 status code error",
			err:  errors.New("HTTP 429 Too Many Requests"),
			want: true,
		},
		{
			name: "rate_limited in middle of message",
			err:  errors.New("request failed with rate_limited response"),
			want: true,
		},
		{
			name: "429 in middle of message",
			err:  errors.New("got status 429 from server"),
			want: true,
		},
		{
			name: "non-rate-limit error",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "permission denied error",
			err:  errors.New("permission denied"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRateLimitError(tt.err)
			if got != tt.want {
				t.Errorf("isRateLimitError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
