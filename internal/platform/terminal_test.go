package platform

import (
	"net/http/httptest"
	"testing"
)

func TestTerminalOrigin(t *testing.T) {
	t.Setenv("KZURE_ORIGIN", "https://cloud.example.test")
	for _, tt := range []struct {
		origin  string
		allowed bool
	}{{"https://cloud.example.test", true}, {"http://localhost:5173", false}, {"", false}, {"https://cloud.example.test.evil.test", false}, {"http://cloud.example.test", false}, {"null", false}} {
		r := httptest.NewRequest("GET", "http://api/api/terminal-sessions/test/stream", nil)
		r.Header.Set("Origin", tt.origin)
		if got := terminalOrigin(r); got != tt.allowed {
			t.Errorf("origin %q: got %v, want %v", tt.origin, got, tt.allowed)
		}
	}
}

func TestTerminalSizeBounds(t *testing.T) {
	for _, tt := range []struct {
		cols, rows int
		valid      bool
	}{{80, 24, true}, {2, 2, true}, {500, 500, true}, {0, 24, false}, {80, -1, false}, {501, 24, false}, {80, 501, false}} {
		if got := validTerminalSize(tt.cols, tt.rows); got != tt.valid {
			t.Errorf("%dx%d: got %v", tt.cols, tt.rows, got)
		}
	}
}
