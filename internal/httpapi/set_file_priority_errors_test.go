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
