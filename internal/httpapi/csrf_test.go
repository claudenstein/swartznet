package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLoopbackHostHeader(t *testing.T) {
	for _, tc := range []struct {
		host string
		ok   bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"Localhost:7654", true},
		{"127.0.0.1", true},
		{"127.0.0.1:7654", true},
		{"127.0.0.2:99", true}, // the whole /8 is loopback
		{"[::1]:7654", true},
		{"::1", true},
		{"", false},
		{"evil.example", false},
		{"evil.example:7654", false},
		{"127.0.0.1.nip.io", false}, // resolves to loopback but is not trusted
		{"192.168.1.10:7654", false},
		{"[fe80::1]:7654", false},
		{"0.0.0.0:7654", false},
	} {
		if got := isLoopbackHostHeader(tc.host); got != tc.ok {
			t.Errorf("isLoopbackHostHeader(%q) = %v, want %v", tc.host, got, tc.ok)
		}
	}
}

func TestIsLoopbackOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin string
		ok     bool
	}{
		{"http://localhost:7654", true},
		{"http://127.0.0.1:7654", true},
		{"http://[::1]:7654", true},
		{"http://localhost", true},
		{"http://evil.example", false},
		{"null", false},      // host-less: fail closed
		{"not a url", false}, // unparseable: fail closed
		{"", false},
	} {
		if got := isLoopbackOrigin(tc.origin); got != tc.ok {
			t.Errorf("isLoopbackOrigin(%q) = %v, want %v", tc.origin, got, tc.ok)
		}
	}
}

func TestCSRFGuard(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	guarded := withCSRFGuard(next)

	for _, tc := range []struct {
		name       string
		method     string
		host       string
		origin     string
		referer    string
		wantStatus int
		wantBody   string
	}{
		{"GET loopback Host passes even with evil origin", "GET", "localhost:7654", "http://evil.example", "", 200, ""},
		{"GET non-loopback Host blocked (DNS-rebind)", "GET", "evil.example:7654", "", "", 403, "forbidden: non-loopback Host"},
		{"HEAD loopback Host passes", "HEAD", "localhost:7654", "", "", 200, ""},
		{"HEAD non-loopback Host blocked", "HEAD", "evil.example", "http://evil.example", "", 403, "forbidden: non-loopback Host"},
		{"OPTIONS non-loopback Host blocked", "OPTIONS", "evil.example", "", "", 403, "forbidden: non-loopback Host"},
		{"POST clean loopback", "POST", "localhost:7654", "", "", 200, ""},
		{"POST loopback origin passes", "POST", "localhost:7654", "http://localhost:7654", "", 200, ""},
		{"POST 127.0.0.1 origin passes", "POST", "127.0.0.1:7654", "http://127.0.0.1:7654", "", 200, ""},
		{"POST non-loopback Host", "POST", "evil.example:7654", "", "", 403, "forbidden: non-loopback Host"},
		{"POST rebind-shaped Host", "POST", "127.0.0.1.nip.io:7654", "", "", 403, "forbidden: non-loopback Host"},
		{"POST evil origin", "POST", "localhost:7654", "http://evil.example", "", 403, "forbidden: cross-origin request"},
		{"POST null origin fails closed", "POST", "localhost:7654", "null", "", 403, "forbidden: cross-origin request"},
		{"POST evil origin wins over loopback referer", "POST", "localhost:7654", "http://evil.example", "http://localhost:7654/", 403, "forbidden: cross-origin request"},
		{"POST evil referer no origin", "POST", "localhost:7654", "", "http://evil.example/page", 403, "forbidden: cross-origin Referer"},
		{"POST loopback referer no origin", "POST", "localhost:7654", "", "http://localhost:7654/", 200, ""},
		{"DELETE guarded too", "DELETE", "localhost:7654", "http://evil.example", "", 403, "forbidden: cross-origin request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://placeholder/x", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantBody != "" && strings.TrimSpace(rec.Body.String()) != tc.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

// TestCSRFRejectionNeverReadsBody pins the middleware order rationale: a
// CSRF-403 must be produced without draining the request body.
func TestCSRFRejectionNeverReadsBody(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := withMaxBodyBytes(withCSRFGuard(next), maxRequestBody)
	body := &countingReader{}
	req := httptest.NewRequest("POST", "http://placeholder/x", body)
	req.Host = "localhost:7654"
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if body.reads != 0 {
		t.Fatalf("guard read the body %d times; rejection must not read", body.reads)
	}
}

type countingReader struct{ reads int }

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	return 0, nil
}
