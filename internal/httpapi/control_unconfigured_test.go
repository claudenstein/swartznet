package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// startUnconfiguredServer wires up a server with no torrent
// controller so every /torrents/* and /config/* handler hits its
// "torrent controller not configured" 503 guard.
func startUnconfiguredServer(t *testing.T) string {
	t.Helper()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), httpapi.Options{})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return "http://" + s.Addr()
}

func TestControlEndpointsReturn503WhenUnconfigured(t *testing.T) {
	t.Parallel()
	base := startUnconfiguredServer(t)
	const ih = "1111111111111111111111111111111111111111"

	cases := []struct {
		name, method, path, body string
	}{
		{"GET rate-limit", http.MethodGet, "/config/rate-limit", ""},
		{"POST rate-limit", http.MethodPost, "/config/rate-limit", `{"upload_bps":1}`},
		{"GET queue", http.MethodGet, "/config/queue", ""},
		{"POST queue", http.MethodPost, "/config/queue", `{"max_active_downloads":1}`},
		{"POST indexing", http.MethodPost, "/torrents/" + ih + "/indexing", `{"enabled":true}`},
		{"GET files", http.MethodGet, "/torrents/" + ih + "/files", ""},
		{"POST file priority", http.MethodPost, "/torrents/" + ih + "/files/0/priority", `{"priority":"high"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var body *strings.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			} else {
				body = strings.NewReader("")
			}
			req, err := http.NewRequest(tc.method, base+tc.path, body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", resp.StatusCode)
			}
		})
	}
}

// TestControlEndpointsBadInfoHash hits the path-validation 400
// branch on each /torrents/{infohash}/* handler. The controller is
// configured (so we get past the 503 guard) and the path infohash
// is malformed.
func TestControlEndpointsBadInfoHash(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})

	cases := []struct {
		name, method, path, body string
	}{
		{"GET files bad hash", http.MethodGet, "/torrents/zzzz/files", ""},
		{"POST file priority bad hash", http.MethodPost, "/torrents/zzzz/files/0/priority", `{"priority":"high"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var body *strings.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			} else {
				body = strings.NewReader("")
			}
			req, err := http.NewRequest(tc.method, base+tc.path, body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

// TestSetFilePriorityBadJSON covers the json.Decode error branch
// on POST /torrents/{infohash}/files/{index}/priority — the
// controller is wired but the body is unparseable.
func TestSetFilePriorityBadJSON(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})
	resp, err := http.Post(base+"/torrents/"+validIH+"/files/0/priority", "application/json",
		strings.NewReader("{nope"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSetTorrentIndexingBadJSON covers the json.Decode error
// branch on POST /torrents/{infohash}/indexing.
func TestSetTorrentIndexingBadJSON(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})
	resp, err := http.Post(base+"/torrents/"+validIH+"/indexing", "application/json",
		strings.NewReader("{nope"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestSetQueueBadJSON covers the json.Decode error branch on
// POST /config/queue.
func TestSetQueueBadJSON(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})
	resp, err := http.Post(base+"/config/queue", "application/json", strings.NewReader("{nope"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
