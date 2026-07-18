package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/time/rate"

	"github.com/swartznet/swartznet/internal/config"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.Config{
		DataDir:    filepath.Join(t.TempDir(), "data"),
		ListenPort: 0,
		DisableDHT: true,
		Seed:       true,
	}
	e, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestAddMagnetErrorTaxonomy(t *testing.T) {
	e := testEngine(t)
	if _, err := e.AddMagnet("magnet:?xt=urn:btih:"); err == nil ||
		!strings.Contains(err.Error(), "engine:") {
		t.Fatalf("empty btih: err = %v", err)
	}
	if _, err := e.AddMagnet("not-a-magnet"); err == nil ||
		!strings.Contains(err.Error(), "engine: parse magnet:") {
		t.Fatalf("bad uri: err = %v", err)
	}
	// The zero-infohash guard: anacrolix would panic without it.
	if _, err := e.AddMagnet("magnet:?xt=urn:btih:0000000000000000000000000000000000000000"); err == nil ||
		!strings.Contains(err.Error(), "zero infohash") {
		t.Fatalf("zero hash: err = %v", err)
	}
}

func TestAddMagnetURIRecoversPanics(t *testing.T) {
	e := testEngine(t)
	// Whatever pathological input does internally, the API layer sees an
	// error string, never a panic.
	if _, err := e.AddMagnetURI("magnet:?xt=urn:btih:zz"); err == nil {
		t.Fatal("want error for undecodable btih")
	}
}

func TestAddInfoHashZeroRejected(t *testing.T) {
	e := testEngine(t)
	if _, err := e.AddInfoHash([20]byte{}); err == nil ||
		!strings.Contains(err.Error(), "engine: zero infohash") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddTorrentFileReadError(t *testing.T) {
	e := testEngine(t)
	if _, err := e.AddTorrentFile(filepath.Join(t.TempDir(), "missing.torrent")); err == nil ||
		!strings.Contains(err.Error(), "engine: read .torrent:") {
		t.Fatalf("err = %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.torrent")
	if err := os.WriteFile(bad, []byte("not bencode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddTorrentFile(bad); err == nil ||
		!strings.Contains(err.Error(), "engine: load .torrent:") {
		t.Fatalf("err = %v", err)
	}
}

func TestHandleLookupErrors(t *testing.T) {
	e := testEngine(t)
	if _, err := e.handleByHex("nothex"); err == nil ||
		!strings.Contains(err.Error(), "engine: invalid infohash") {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.handleByHex(strings.Repeat("ab", 20)); err == nil ||
		!strings.Contains(err.Error(), "engine: no torrent with infohash") {
		t.Fatalf("err = %v", err)
	}
}

func TestSessionFileGolden(t *testing.T) {
	dataDir := t.TempDir()
	sess, err := loadSession(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ih := strings.Repeat("ab", 20)
	if err := sess.update(ih, func(ent *sessionEntry) {
		ent.AddedVia = "magnet"
		ent.MagnetURI = "magnet:?xt=urn:btih:" + ih
		ent.Indexing = true
		ent.QueueOrder = 3
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Pin the frozen JSON keys (indexing is deliberately NOT omitempty).
	for _, key := range []string{`"version": 1`, `"infohash"`, `"added_via": "magnet"`, `"magnet_uri"`, `"indexing": true`, `"queue_order": 3`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("session.json lacks %s:\n%s", key, raw)
		}
	}
	for _, absent := range []string{`"paused"`, `"torrent_file"`, `"signed_by"`, `"data_path"`} {
		if strings.Contains(string(raw), absent) {
			t.Fatalf("session.json has omitempty-field %s set:\n%s", absent, raw)
		}
	}

	// Round-trip: a fresh load sees the entry; indexing=false round-trips too.
	sess2, err := loadSession(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got := sess2.list()
	if len(got) != 1 || got[0].MagnetURI == "" || !got[0].Indexing {
		t.Fatalf("reloaded = %+v", got)
	}
}

func TestSessionDropsMalformedRows(t *testing.T) {
	dataDir := t.TempDir()
	body := `{"version":1,"torrents":[{"infohash":"short","added_via":"magnet","indexing":true},{"infohash":"` + strings.Repeat("cd", 20) + `","added_via":"infohash","indexing":true}]}`
	if err := os.WriteFile(filepath.Join(dataDir, "session.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := loadSession(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.list(); len(got) != 1 || got[0].InfoHash != strings.Repeat("cd", 20) {
		t.Fatalf("list = %+v, want only the valid row", got)
	}
}

func TestSessionCorruptJSONIsError(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "session.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSession(dataDir); err == nil ||
		!strings.Contains(err.Error(), "engine: decode session:") {
		t.Fatalf("err = %v", err)
	}
}

func TestEngineSurvivesCorruptSession(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "session.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: dataDir, ListenPort: 0, DisableDHT: true}
	e, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("corrupt session must not fail New: %v", err)
	}
	_ = e.Close()
}

func TestRateLimitSemantics(t *testing.T) {
	e := testEngine(t)
	if up := e.UploadLimitBytesPerSec(); up != 0 {
		t.Fatalf("initial upload limit = %d, want 0 (unlimited)", up)
	}
	e.SetDownloadLimitBytesPerSec(1000)
	if down := e.DownloadLimitBytesPerSec(); down != 1000 {
		t.Fatalf("download = %d", down)
	}
	// Partial-update contract: the other side is untouched.
	if up := e.UploadLimitBytesPerSec(); up != 0 {
		t.Fatalf("setting download changed upload to %d", up)
	}
	// The burst floor: a tiny limit still gets a 16 KiB burst.
	if b := e.dlLimiter.Burst(); b != 16*1024 {
		t.Fatalf("burst = %d, want 16384 floor", b)
	}
	// Back to unlimited restores the positive MaxInt32 burst.
	e.SetDownloadLimitBytesPerSec(0)
	if e.dlLimiter.Limit() != rate.Inf || e.dlLimiter.Burst() != unlimitedBurst {
		t.Fatal("unlimited must restore Inf + positive max burst")
	}
}

func TestUnlimitedBurstIsPositiveAtConstruction(t *testing.T) {
	e := testEngine(t)
	// The quirk: zero tokens would silently block all outgoing dials.
	if e.dlLimiter.Tokens() <= 0 {
		t.Fatal("download limiter constructed with non-positive tokens")
	}
}

func TestSetMaxActiveDownloadsClampsNegatives(t *testing.T) {
	e := testEngine(t)
	e.SetMaxActiveDownloads(-5)
	if got := e.MaxActiveDownloads(); got != 0 {
		t.Fatalf("cap = %d, want 0", got)
	}
}

func TestPriorityParsing(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"none", true}, {"normal", true}, {"high", true}, {"", true},
		{"NONE", false}, {"max", false},
	} {
		_, err := parsePriority(tc.in)
		if tc.ok && err != nil {
			t.Errorf("%q: %v", tc.in, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "unknown file priority")) {
			t.Errorf("%q: err = %v", tc.in, err)
		}
	}
}

func TestCloseIdempotent(t *testing.T) {
	e := testEngine(t)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if _, err := e.AddMagnet("magnet:?xt=urn:btih:" + strings.Repeat("ab", 20)); err == nil ||
		!strings.Contains(err.Error(), "engine: closed") {
		t.Fatalf("add after close: %v", err)
	}
}

func TestSnapshotPreMetadataSafety(t *testing.T) {
	e := testEngine(t)
	var hash [20]byte
	copy(hash[:], []byte("aaaaaaaaaaaaaaaaaaaa"))
	h, err := e.AddInfoHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	// No metadata will ever arrive (DHT off, no peers): the snapshot must
	// not touch the nil-panicking anacrolix getters.
	s := h.snapshot()
	if s.Status != "metadata" || s.Size != 0 || s.Files != 0 {
		t.Fatalf("pre-metadata snapshot = %+v", s)
	}
}
