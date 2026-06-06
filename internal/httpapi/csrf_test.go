package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestCSRFGuard exercises the cross-origin / DNS-rebinding guard on
// a state-mutating endpoint. POST /flag is convenient: it requires
// only an attached tracker (none here → would normally reach the
// handler and 503), so a 403 from the guard is unambiguous proof
// the request was rejected before reaching the handler.
func TestCSRFGuard(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})
	client := &http.Client{}

	mutate := func(t *testing.T, host, origin, referer string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, base+"/flag",
			strings.NewReader(`{"infohash":"1111111111111111111111111111111111111111"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	tests := []struct {
		name      string
		host      string // overrides Host header; "" keeps the real loopback host
		origin    string
		referer   string
		forbidden bool
	}{
		{name: "no origin loopback host passes guard", forbidden: false},
		{name: "loopback origin passes guard", origin: "http://127.0.0.1:7654", forbidden: false},
		{name: "localhost origin passes guard", origin: "http://localhost:7654", forbidden: false},
		{name: "loopback referer passes guard", referer: "http://127.0.0.1:7654/", forbidden: false},
		{name: "cross-origin rejected", origin: "http://evil.example", forbidden: true},
		{name: "cross-origin referer rejected", referer: "http://evil.example/x", forbidden: true},
		{name: "dns rebinding host rejected", host: "evil.example", forbidden: true},
		{name: "dns rebinding host with port rejected", host: "evil.example:7654", forbidden: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			code := mutate(t, tc.host, tc.origin, tc.referer)
			if tc.forbidden {
				if code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403 (request should be rejected by guard)", code)
				}
				return
			}
			// Allowed by the guard: it then reaches the handler,
			// which 503s because no tracker is configured. The one
			// status the guard must never produce here is 403.
			if code == http.StatusForbidden {
				t.Fatalf("status = 403, request should have passed the guard")
			}
		})
	}
}

// TestCSRFGuardAllowsReads confirms GET requests are never blocked
// by the guard even from a cross-origin page (reads do not mutate
// state, and blocking them would break legitimate dashboards).
func TestCSRFGuardAllowsReads(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})
	client := &http.Client{}

	req, err := http.NewRequest(http.MethodGet, base+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://evil.example")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("GET /healthz blocked by CSRF guard; reads must be exempt")
	}
}
