package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/swartznet/swartznet/internal/httpapi"
)

// statefulController tracks rate-limit and queue-depth settings so
// the GET-after-POST round-trip tests can verify the handlers
// actually push values into the controller and read them back. The
// torrent-action methods still record into actions[] like the
// shared fakeController so failure modes (errors, bad-priority)
// can be wired in via the err* fields.
type statefulController struct {
	mu       sync.Mutex
	upBPS    int64
	downBPS  int64
	maxAct   int
	files    []httpapi.TorrentFile
	filesErr error
	idxErr   error
	prioErr  error
	actions  []string
}

func (s *statefulController) TorrentSnapshots() []httpapi.TorrentSnapshot { return nil }
func (s *statefulController) AddMagnetURI(string) (string, error) {
	return "1111111111111111111111111111111111111111", nil
}
func (s *statefulController) PauseTorrent(string) error  { return nil }
func (s *statefulController) ResumeTorrent(string) error { return nil }
func (s *statefulController) RemoveTorrent(string) error { return nil }
func (s *statefulController) SetTorrentIndexing(ih string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	verb := "indexing_off"
	if enabled {
		verb = "indexing_on"
	}
	s.actions = append(s.actions, verb+":"+ih)
	return s.idxErr
}
func (s *statefulController) TorrentFiles(ih string) ([]httpapi.TorrentFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, "files:"+ih)
	return s.files, s.filesErr
}
func (s *statefulController) SetFilePriority(ih string, idx int, prio string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, "prio:"+prio+":"+ih)
	return s.prioErr
}

func (s *statefulController) UploadLimitBytesPerSec() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upBPS
}
func (s *statefulController) DownloadLimitBytesPerSec() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.downBPS
}
func (s *statefulController) SetUploadLimitBytesPerSec(bps int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upBPS = bps
}
func (s *statefulController) SetDownloadLimitBytesPerSec(bps int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downBPS = bps
}
func (s *statefulController) MaxActiveDownloads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxAct
}
func (s *statefulController) SetMaxActiveDownloads(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxAct = n
}

const validIH = "1111111111111111111111111111111111111111"

func TestHTTPRateLimitRoundTrip(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})

	body, _ := json.Marshal(httpapi.RateLimitRequest{UploadBytesPerSec: 1000, DownloadBytesPerSec: 2000})
	resp, err := http.Post(base+"/config/rate-limit", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST status=%d body=%s", resp.StatusCode, raw)
	}
	// Response body echoes the new values via handleGetRateLimit.
	var got httpapi.RateLimitResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.UploadBytesPerSec != 1000 || got.DownloadBytesPerSec != 2000 {
		t.Errorf("POST echo = %+v, want {1000, 2000}", got)
	}

	// Independent GET confirms persistence in the controller.
	resp2, err := http.Get(base + "/config/rate-limit")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var got2 httpapi.RateLimitResponse
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	if got2.UploadBytesPerSec != 1000 || got2.DownloadBytesPerSec != 2000 {
		t.Errorf("GET = %+v, want {1000, 2000}", got2)
	}
}

func TestHTTPRateLimitBadJSON(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{Control: &statefulController{}})
	resp, err := http.Post(base+"/config/rate-limit", "application/json", strings.NewReader("{nope"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPQueueRoundTrip(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})

	body, _ := json.Marshal(httpapi.QueueRequest{MaxActiveDownloads: 9})
	resp, err := http.Post(base+"/config/queue", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST status=%d body=%s", resp.StatusCode, raw)
	}
	var got httpapi.QueueResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.MaxActiveDownloads != 9 {
		t.Errorf("POST echo MaxActiveDownloads = %d, want 9", got.MaxActiveDownloads)
	}

	resp2, err := http.Get(base + "/config/queue")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var got2 httpapi.QueueResponse
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	if got2.MaxActiveDownloads != 9 {
		t.Errorf("GET MaxActiveDownloads = %d, want 9", got2.MaxActiveDownloads)
	}
}

func TestHTTPSetTorrentIndexing(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})

	body, _ := json.Marshal(httpapi.IndexingRequest{Enabled: true})
	resp, err := http.Post(base+"/torrents/"+validIH+"/indexing", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || got["enabled"] != true || got["infohash"] != validIH {
		t.Errorf("response = %v, missing expected fields", got)
	}
	if len(sc.actions) != 1 || sc.actions[0] != "indexing_on:"+validIH {
		t.Errorf("actions = %v, want [indexing_on:%s]", sc.actions, validIH)
	}
}

func TestHTTPSetTorrentIndexingBadHash(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{Control: &statefulController{}})
	body, _ := json.Marshal(httpapi.IndexingRequest{Enabled: true})
	resp, err := http.Post(base+"/torrents/not-hex/indexing", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTPListTorrentFiles(t *testing.T) {
	t.Parallel()
	sc := &statefulController{
		files: []httpapi.TorrentFile{
			{Index: 0, Path: "a.txt", Length: 10, BytesCompleted: 10, Progress: 1.0, Priority: "normal"},
			{Index: 1, Path: "b.txt", Length: 20, Priority: "high"},
		},
	}
	base := startServer(t, httpapi.Options{Control: sc})

	resp, err := http.Get(base + "/torrents/" + validIH + "/files")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got httpapi.FilesListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.InfoHash != validIH {
		t.Errorf("InfoHash = %q, want %q", got.InfoHash, validIH)
	}
	if len(got.Files) != 2 || got.Files[0].Path != "a.txt" || got.Files[1].Path != "b.txt" {
		t.Errorf("Files = %+v", got.Files)
	}
}

func TestHTTPSetFilePriority(t *testing.T) {
	t.Parallel()
	sc := &statefulController{}
	base := startServer(t, httpapi.Options{Control: sc})

	body, _ := json.Marshal(httpapi.FilePriorityRequest{Priority: "high"})
	resp, err := http.Post(base+"/torrents/"+validIH+"/files/3/priority", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || got["priority"] != "high" {
		t.Errorf("response = %v", got)
	}
	// Index encodes to a JSON number: assert via float64.
	if idx, ok := got["index"].(float64); !ok || int(idx) != 3 {
		t.Errorf("index = %v, want 3", got["index"])
	}
	if len(sc.actions) != 1 || sc.actions[0] != "prio:high:"+validIH {
		t.Errorf("actions = %v", sc.actions)
	}
}

func TestHTTPSetFilePriorityBadIndex(t *testing.T) {
	t.Parallel()
	base := startServer(t, httpapi.Options{Control: &statefulController{}})
	body, _ := json.Marshal(httpapi.FilePriorityRequest{Priority: "high"})
	resp, err := http.Post(base+"/torrents/"+validIH+"/files/-1/priority", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// PathValue("index") = "-1"; Sscanf("%d") parses it but our handler
	// rejects negatives explicitly.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
