package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// fakeControl is an in-package TorrentController double.
type fakeControl struct {
	snaps     []TorrentSnapshot
	files     []TorrentFile
	filesErr  error
	upBps     int64
	downBps   int64
	maxActive int
	actions   []string
}

func (f *fakeControl) AddMagnetURI(uri string) (string, error) {
	if strings.Contains(uri, "bad") {
		return "", fmt.Errorf("engine: parse magnet: synthetic")
	}
	return strings.Repeat("ab", 20), nil
}
func (f *fakeControl) TorrentSnapshots() []TorrentSnapshot { return f.snaps }
func (f *fakeControl) TorrentFiles(string) ([]TorrentFile, error) {
	return f.files, f.filesErr
}
func (f *fakeControl) SetFilePriority(ih string, idx int, p string) error {
	if p == "bogus" {
		return fmt.Errorf("engine: unknown file priority %q (want none/normal/high)", p)
	}
	f.actions = append(f.actions, fmt.Sprintf("prio:%s:%d:%s", ih, idx, p))
	return nil
}
func (f *fakeControl) PauseTorrent(ih string) error {
	f.actions = append(f.actions, "pause:"+ih)
	return nil
}
func (f *fakeControl) ResumeTorrent(ih string) error {
	f.actions = append(f.actions, "resume:"+ih)
	return nil
}
func (f *fakeControl) RemoveTorrent(ih string, forget bool) error {
	f.actions = append(f.actions, fmt.Sprintf("remove:%s:%v", ih, forget))
	return nil
}
func (f *fakeControl) SetTorrentIndexing(ih string, enabled bool) error {
	f.actions = append(f.actions, fmt.Sprintf("indexing:%s:%v", ih, enabled))
	return nil
}
func (f *fakeControl) UploadLimitBytesPerSec() int64       { return f.upBps }
func (f *fakeControl) DownloadLimitBytesPerSec() int64     { return f.downBps }
func (f *fakeControl) SetUploadLimitBytesPerSec(b int64)   { f.upBps = b }
func (f *fakeControl) SetDownloadLimitBytesPerSec(b int64) { f.downBps = b }
func (f *fakeControl) MaxActiveDownloads() int             { return f.maxActive }
func (f *fakeControl) SetMaxActiveDownloads(n int) {
	if n < 0 {
		n = 0
	}
	f.maxActive = n
}

func startControlServer(t *testing.T, ctl *fakeControl) string {
	t.Helper()
	opts := Options{}
	if ctl != nil {
		opts.Adder = ctl
		opts.Control = ctl
	}
	s := NewWithOptions("localhost:0", slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(t.Context()) })
	return s.Addr()
}

func doJSON(t *testing.T, method, url string, body string) (*http.Response, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestNilCollaborators503(t *testing.T) {
	addr := startControlServer(t, nil)
	for _, tc := range []struct {
		method, path, want string
	}{
		{"POST", "/torrent", "torrent adder not configured"},
		{"GET", "/torrents", "torrent controller not configured"},
		{"GET", "/torrents/" + strings.Repeat("ab", 20) + "/files", "torrent controller not configured"},
		{"GET", "/config/rate-limit", "torrent controller not configured"},
		{"GET", "/config/queue", "torrent controller not configured"},
	} {
		resp, body := doJSON(t, tc.method, "http://"+addr+tc.path, "{}")
		if resp.StatusCode != 503 || !strings.Contains(body, tc.want) {
			t.Errorf("%s %s = %d %q, want 503 %q", tc.method, tc.path, resp.StatusCode, body, tc.want)
		}
	}
}

func TestAddTorrentEndpoint(t *testing.T) {
	addr := startControlServer(t, &fakeControl{})
	resp, body := doJSON(t, "POST", "http://"+addr+"/torrent", `{"uri":"magnet:?xt=urn:btih:abcd"}`)
	if resp.StatusCode != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, strings.Repeat("ab", 20)) {
		t.Fatalf("add = %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, "POST", "http://"+addr+"/torrent", `{"uri":""}`)
	if resp.StatusCode != 400 || !strings.Contains(body, "missing 'uri' field") {
		t.Fatalf("empty uri = %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, "POST", "http://"+addr+"/torrent", `{nope`)
	if resp.StatusCode != 400 || !strings.Contains(body, "bad json:") {
		t.Fatalf("bad json = %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, "POST", "http://"+addr+"/torrent", `{"uri":"bad-magnet"}`)
	if resp.StatusCode != 400 || !strings.Contains(body, "add: engine: parse magnet:") {
		t.Fatalf("engine error = %d %s", resp.StatusCode, body)
	}
}

func TestTorrentsListNeverNull(t *testing.T) {
	addr := startControlServer(t, &fakeControl{})
	_, body := doJSON(t, "GET", "http://"+addr+"/torrents", "")
	if strings.TrimSpace(body) != `{"torrents":[]}` {
		t.Fatalf("empty list = %s, want [] never null", body)
	}
}

func TestInfohashPathValidation(t *testing.T) {
	addr := startControlServer(t, &fakeControl{})
	for _, bad := range []string{"short", strings.Repeat("zz", 20)} {
		resp, body := doJSON(t, "GET", "http://"+addr+"/torrents/"+bad+"/files", "")
		if resp.StatusCode != 400 || !strings.Contains(body, "infohash must be 40 hex characters") {
			t.Errorf("ih %q = %d %s", bad, resp.StatusCode, body)
		}
	}
}

func TestFilePriorityEndpoint(t *testing.T) {
	ctl := &fakeControl{}
	addr := startControlServer(t, ctl)
	ih := strings.Repeat("ab", 20)
	resp, _ := doJSON(t, "POST", "http://"+addr+"/torrents/"+ih+"/files/3/priority", `{"priority":"none"}`)
	if resp.StatusCode != 200 || len(ctl.actions) != 1 || ctl.actions[0] != "prio:"+ih+":3:none" {
		t.Fatalf("resp %d actions %v", resp.StatusCode, ctl.actions)
	}
	resp, body := doJSON(t, "POST", "http://"+addr+"/torrents/"+ih+"/files/-1/priority", `{"priority":"none"}`)
	// -1 is caught by the mux pattern or the Atoi guard, either way 4xx.
	if resp.StatusCode == 200 {
		t.Fatalf("negative index accepted: %s", body)
	}
	resp, body = doJSON(t, "POST", "http://"+addr+"/torrents/"+ih+"/files/3/priority", `{"priority":"bogus"}`)
	if resp.StatusCode != 400 || !strings.Contains(body, "unknown file priority") {
		t.Fatalf("bogus prio = %d %s", resp.StatusCode, body)
	}
}

func TestTorrentActions(t *testing.T) {
	ctl := &fakeControl{}
	addr := startControlServer(t, ctl)
	ih := strings.Repeat("ab", 20)
	doJSON(t, "POST", "http://"+addr+"/torrents/"+ih+"/pause", "")
	doJSON(t, "POST", "http://"+addr+"/torrents/"+ih+"/resume", "")
	resp, body := doJSON(t, "DELETE", "http://"+addr+"/torrents/"+ih, "")
	if resp.StatusCode != 200 || !strings.Contains(body, `"action":"remove"`) {
		t.Fatalf("remove = %d %s", resp.StatusCode, body)
	}
	want := []string{"pause:" + ih, "resume:" + ih, "remove:" + ih + ":false"}
	if len(ctl.actions) != 3 || ctl.actions[0] != want[0] || ctl.actions[1] != want[1] || ctl.actions[2] != want[2] {
		t.Fatalf("actions = %v", ctl.actions)
	}
	// ?forget=1 threads the forget flag through.
	ctl.actions = nil
	doJSON(t, "DELETE", "http://"+addr+"/torrents/"+ih+"?forget=1", "")
	if len(ctl.actions) != 1 || ctl.actions[0] != "remove:"+ih+":true" {
		t.Fatalf("forget actions = %v", ctl.actions)
	}
}

func TestSetIndexingEndpoint(t *testing.T) {
	ctl := &fakeControl{}
	addr := startControlServer(t, ctl)
	ih := strings.Repeat("ab", 20)
	resp, body := doJSON(t, "POST", "http://"+addr+"/torrents/"+ih+"/indexing", `{"enabled":false}`)
	if resp.StatusCode != 200 || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("indexing = %d %s", resp.StatusCode, body)
	}
	if len(ctl.actions) != 1 || ctl.actions[0] != "indexing:"+ih+":false" {
		t.Fatalf("actions = %v", ctl.actions)
	}
}

// TestRateLimitMergeSemantics is the §6 DoD pin: PATCH with only one field
// set leaves the other untouched.
func TestRateLimitMergeSemantics(t *testing.T) {
	ctl := &fakeControl{upBps: 555}
	addr := startControlServer(t, ctl)

	resp, body := doJSON(t, "PATCH", "http://"+addr+"/config/rate-limit", `{"download_bps":1000}`)
	if resp.StatusCode != 200 {
		t.Fatalf("patch = %d %s", resp.StatusCode, body)
	}
	var got RateLimitResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.DownloadBps != 1000 || got.UploadBps != 555 {
		t.Fatalf("merge broke: %+v (upload must stay 555)", got)
	}

	// POST rides the same merge handler.
	_, body = doJSON(t, "POST", "http://"+addr+"/config/rate-limit", `{"upload_bps":0}`)
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.UploadBps != 0 || got.DownloadBps != 1000 {
		t.Fatalf("post-merge: %+v", got)
	}
}

func TestQueueConfigEndpoint(t *testing.T) {
	ctl := &fakeControl{}
	addr := startControlServer(t, ctl)
	_, body := doJSON(t, "PATCH", "http://"+addr+"/config/queue", `{"max_active_downloads":-5}`)
	var got QueueConfigResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.MaxActiveDownloads != 0 {
		t.Fatalf("negative cap echoed as %d, want clamped 0", got.MaxActiveDownloads)
	}
	// Empty body object is a no-op.
	doJSON(t, "PATCH", "http://"+addr+"/config/queue", `{"max_active_downloads":4}`)
	_, body = doJSON(t, "PATCH", "http://"+addr+"/config/queue", `{}`)
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.MaxActiveDownloads != 4 {
		t.Fatalf("no-op patch changed cap to %d", got.MaxActiveDownloads)
	}
}

// TestNewRoutesAreCSRFGuarded: the guard wraps the whole mux, so every new
// non-GET route rejects a spoofed Origin.
func TestNewRoutesAreCSRFGuarded(t *testing.T) {
	addr := startControlServer(t, &fakeControl{})
	req, _ := http.NewRequest("POST", "http://"+addr+"/torrent", bytes.NewReader([]byte(`{"uri":"magnet:x"}`)))
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("spoofed-origin POST /torrent = %d, want 403", resp.StatusCode)
	}
}
