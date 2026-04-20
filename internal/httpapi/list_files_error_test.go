package httpapi_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// TestHTTPListTorrentFilesControllerError covers
// handleListTorrentFiles' TorrentFiles-returns-error branch.
// The other tests exercise the happy path, the bad-hash branch,
// and the controller-not-configured branch; only this branch was
// untested.
func TestHTTPListTorrentFilesControllerError(t *testing.T) {
	t.Parallel()
	sc := &statefulController{filesErr: errors.New("metainfo not yet")}
	base := startServer(t, httpapi.Options{Control: sc})

	resp, err := http.Get(base + "/torrents/" + validIH + "/files")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s, want 400", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "metainfo not yet") {
		t.Errorf("response should mention controller error, got %q", raw)
	}
}
