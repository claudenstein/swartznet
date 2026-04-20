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

// TestHTTPSetTorrentIndexingControllerError covers
// handleSetTorrentIndexing's "controller returns error" branch.
// The other tests exercise the happy path, the bad-hash branch,
// and the controller-not-configured branch; only this branch was
// untested.
func TestHTTPSetTorrentIndexingControllerError(t *testing.T) {
	t.Parallel()
	sc := &statefulController{idxErr: errors.New("toggle refused")}
	base := startServer(t, httpapi.Options{Control: sc})

	body, _ := json.Marshal(httpapi.IndexingRequest{Enabled: true})
	resp, err := http.Post(base+"/torrents/"+validIH+"/indexing", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s, want 400", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "toggle refused") {
		t.Errorf("response should mention controller error, got %q", raw)
	}
}
