package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
)

// Common test helpers for the handler tests. Each test builds a
// server with just the collaborators it needs, so handlers can be
// exercised without a live engine or DHT.

// startServer wires a Server at localhost:0, returns the URL
// base ("http://127.0.0.1:NNNN") and a cleanup that stops it.
func startServer(t *testing.T, opts httpapi.Options) string {
	t.Helper()
	s := httpapi.NewWithOptions("localhost:0", silentLogger(), opts)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return "http://" + s.Addr()
}

// openTempIndex creates a Bleve index under t.TempDir() with a
// couple of seed documents so handlers that depend on an index
// have something to return.
func openTempIndex(t *testing.T) *indexer.Index {
	t.Helper()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "ubuntu 24.04",
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexContent(indexer.ContentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		FilePath: "README.md",
		Text:     "the quick brown fox",
	}); err != nil {
		t.Fatal(err)
	}
	return idx
}

// ---------- /index/stats ----------

func TestHTTPIndexStats(t *testing.T) {
	t.Parallel()
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx})

	resp, err := http.Get(base + "/index/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var stats httpapi.IndexStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.TorrentCount != 1 {
		t.Errorf("TorrentCount=%d, want 1", stats.TorrentCount)
	}
	if stats.ContentCount != 1 {
		t.Errorf("ContentCount=%d, want 1", stats.ContentCount)
	}
	if stats.DirBytes <= 0 {
		t.Errorf("DirBytes=%d, want positive", stats.DirBytes)
	}
	if stats.CorpusTextBytes != int64(len("the quick brown fox")) {
		t.Errorf("CorpusTextBytes=%d, want 19", stats.CorpusTextBytes)
	}
	if stats.InflationRatio <= 0 {
		t.Errorf("InflationRatio=%v, want >0", stats.InflationRatio)
	}
}

func TestHTTPIndexStatsUnconfigured(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})
	resp, err := http.Get(base + "/index/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

// ---------- /status ----------

func TestHTTPStatusMinimal(t *testing.T) {
	t.Parallel()
	idx := openTempIndex(t)
	base := startServer(t, httpapi.Options{Index: idx})

	resp, err := http.Get(base + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Local.Indexed {
		t.Error("Local.Indexed = false, want true")
	}
	if out.Local.DocCount == 0 {
		t.Error("Local.DocCount = 0, want non-zero")
	}
}

// ---------- /torrents + /torrents/... ----------

// fakeController satisfies httpapi.TorrentController for the
// list / pause / resume / remove / add tests.
type fakeController struct {
	mu        sync.Mutex
	snapshots []httpapi.TorrentSnapshot
	actions   []action // ordered log of {kind, arg}
	addErr    error
	pauseErr  error
}

type action struct {
	kind string
	arg  string
}

func (f *fakeController) TorrentSnapshots() []httpapi.TorrentSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]httpapi.TorrentSnapshot(nil), f.snapshots...)
}

func (f *fakeController) AddMagnetURI(uri string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action{"add", uri})
	if f.addErr != nil {
		return "", f.addErr
	}
	return "1111111111111111111111111111111111111111", nil
}

func (f *fakeController) PauseTorrent(ih string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action{"pause", ih})
	return f.pauseErr
}

func (f *fakeController) ResumeTorrent(ih string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action{"resume", ih})
	return nil
}

func (f *fakeController) RemoveTorrent(ih string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action{"remove", ih})
	return nil
}

func (f *fakeController) SetTorrentIndexing(ih string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	verb := "index_on"
	if !enabled {
		verb = "index_off"
	}
	f.actions = append(f.actions, action{verb, ih})
	return nil
}

func (f *fakeController) TorrentFiles(ih string) ([]httpapi.TorrentFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action{"files", ih})
	return nil, nil
}

func (f *fakeController) SetFilePriority(ih string, idx int, priority string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action{"file_prio:" + priority, ih})
	return nil
}

func (f *fakeController) lastAction() action {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.actions) == 0 {
		return action{}
	}
	return f.actions[len(f.actions)-1]
}

func TestHTTPListTorrents(t *testing.T) {
	t.Parallel()
	fc := &fakeController{
		snapshots: []httpapi.TorrentSnapshot{
			{InfoHash: "aaaa", Name: "test", Progress: 0.5, Status: "downloading"},
		},
	}
	base := startServer(t, httpapi.Options{Control: fc})

	resp, err := http.Get(base + "/torrents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte(`"infohash":"aaaa"`)) {
		t.Errorf("body missing infohash: %s", body)
	}
}

func TestHTTPPauseResumeRemove(t *testing.T) {
	t.Parallel()
	fc := &fakeController{}
	base := startServer(t, httpapi.Options{Control: fc})

	ih := "1111111111111111111111111111111111111111"
	cases := []struct {
		name   string
		method string
		path   string
		kind   string
	}{
		{"pause", "POST", "/torrents/" + ih + "/pause", "pause"},
		{"resume", "POST", "/torrents/" + ih + "/resume", "resume"},
		{"remove", "DELETE", "/torrents/" + ih, "remove"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, base+tc.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status=%d, want 200", tc.name, resp.StatusCode)
		}
		if got := fc.lastAction(); got.kind != tc.kind || got.arg != ih {
			t.Errorf("%s: got action %+v", tc.name, got)
		}
	}
}

func TestHTTPPauseBadInfohash(t *testing.T) {
	t.Parallel()
	fc := &fakeController{}
	base := startServer(t, httpapi.Options{Control: fc})

	resp, err := http.Post(base+"/torrents/not-hex/pause", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPPauseControllerUnconfigured(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})
	resp, err := http.Post(base+"/torrents/1111111111111111111111111111111111111111/pause", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}

func TestHTTPControlPauseError(t *testing.T) {
	t.Parallel()
	fc := &fakeController{pauseErr: errors.New("stop me")}
	base := startServer(t, httpapi.Options{Control: fc})

	resp, err := http.Post(base+"/torrents/1111111111111111111111111111111111111111/pause", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

// ---------- /torrent (add magnet) ----------

func TestHTTPAddTorrent(t *testing.T) {
	t.Parallel()
	fc := &fakeController{}
	base := startServer(t, httpapi.Options{Adder: fc, Control: fc})

	body, _ := json.Marshal(map[string]any{"uri": "magnet:?xt=urn:btih:1111111111111111111111111111111111111111"})
	resp, err := http.Post(base+"/torrent", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	if got := fc.lastAction(); got.kind != "add" {
		t.Errorf("last action = %+v, want kind=add", got)
	}
}

func TestHTTPAddTorrentBadJSON(t *testing.T) {
	t.Parallel()
	fc := &fakeController{}
	base := startServer(t, httpapi.Options{Adder: fc})
	resp, err := http.Post(base+"/torrent", "application/json", bytes.NewReader([]byte("{not json")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPAddTorrentMissingURI(t *testing.T) {
	t.Parallel()
	fc := &fakeController{}
	base := startServer(t, httpapi.Options{Adder: fc})
	resp, err := http.Post(base+"/torrent", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

// ---------- /confirm + /flag ----------

func TestHTTPConfirmAndFlag(t *testing.T) {
	t.Parallel()
	// No collaborators required: /confirm and /flag are tolerant of
	// missing bloom / tracker / sources and return 200 with ok:false
	// for the degraded path.
	base := startServer(t, httpapi.Options{})

	body := []byte(`{"infohash":"1111111111111111111111111111111111111111"}`)
	for _, path := range []string{"/confirm", "/flag"} {
		resp, err := http.Post(base+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// Degraded mode should still return a valid HTTP status
		// (either 200 "ok" or 503 "not configured" depending on
		// the path). Just assert it is not a server error.
		if resp.StatusCode >= 500 && resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status=%d, want <500 or 503", path, resp.StatusCode)
		}
	}
}

func TestHTTPConfirmBadInfohash(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})
	resp, err := http.Post(base+"/confirm", "application/json", bytes.NewReader([]byte(`{"infohash":"not-hex"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestHTTPConfirmBadJSON(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})
	resp, err := http.Post(base+"/confirm", "application/json", bytes.NewReader([]byte("{not json")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

// ---------- /capabilities ----------

func TestHTTPCapabilitiesNeedSwarm(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{})
	resp, err := http.Get(base + "/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", resp.StatusCode)
	}
}
