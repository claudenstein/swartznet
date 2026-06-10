package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestHTTPSetFilePriorityControllerError covers the
// SetFilePriority-returns-error branch (the fake controller had
// a prioErr field but no test was setting it).
func TestHTTPSetFilePriorityControllerError(t *testing.T) {
	t.Parallel()
	sc := &statefulController{prioErr: errors.New("priority refused")}
	base := startServer(t, httpapi.Options{Control: sc})

	body, _ := json.Marshal(httpapi.FilePriorityRequest{Priority: "low"})
	resp, err := http.Post(base+"/torrents/"+validIH+"/files/0/priority", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s, want 400", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "priority refused") {
		t.Errorf("response should mention controller error, got %q", raw)
	}
}

// TestHTTPSetFilePriorityIndexNotClean rejects file-index path
// segments that are not a clean base-10 integer. The old
// Sscanf("%d") parse accepted trailing garbage ("3abc" → 3); the
// strconv.Atoi parse must 400 on every one of these.
func TestHTTPSetFilePriorityIndexNotClean(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{Control: &statefulController{}})

	cases := []struct {
		name string
		idx  string
	}{
		{"trailing garbage", "3abc"},
		{"hex prefix", "0x10"},
		{"trailing space", "4%20"},
		{"float", "1.5"},
		{"negative", "-1"},
		{"plain garbage", "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, _ := json.Marshal(httpapi.FilePriorityRequest{Priority: "normal"})
			resp, err := http.Post(base+"/torrents/"+validIH+"/files/"+tc.idx+"/priority", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				raw, _ := io.ReadAll(resp.Body)
				t.Errorf("index %q: status=%d body=%s, want 400", tc.idx, resp.StatusCode, raw)
			}
		})
	}
}

// TestHTTPSetFilePriorityBadJSON covers the json.Decode error
// branch — empty body / non-JSON should yield 400.
func TestHTTPSetFilePriorityBadJSON(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{Control: &statefulController{}})
	resp, err := http.Post(base+"/torrents/"+validIH+"/files/0/priority", "application/json", strings.NewReader("{nope"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
