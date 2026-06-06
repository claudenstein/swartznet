package httpapi

import "testing"

func TestIsLoopbackHostHeader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.1:7654", true},
		{"127.5.5.5", true}, // entire 127.0.0.0/8 is loopback
		{"localhost", true},
		{"localhost:7654", true},
		{"LOCALHOST", true}, // case-insensitive
		{"::1", true},
		{"[::1]:7654", true},
		{"", false},
		{"evil.example", false},
		{"evil.example:7654", false},
		{"10.0.0.1", false},
		{"0.0.0.0", false},
		{"192.168.1.5:7654", false},
	}
	for _, tc := range tests {
		if got := isLoopbackHostHeader(tc.host); got != tc.want {
			t.Errorf("isLoopbackHostHeader(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestIsLoopbackOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://127.0.0.1:7654", true},
		{"http://localhost:7654", true},
		{"https://localhost", true},
		{"http://[::1]:7654", true},
		{"http://evil.example", false},
		{"http://evil.example:7654", false},
		{"", false},     // empty
		{"null", false}, // sandboxed/file origins send "null"
		{"garbage", false},
	}
	for _, tc := range tests {
		if got := isLoopbackOrigin(tc.origin); got != tc.want {
			t.Errorf("isLoopbackOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
		}
	}
}
