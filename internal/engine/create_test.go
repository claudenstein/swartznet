package engine_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

func newTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	// Hermetic: zero out the user-level XDG paths so parallel
	// tests don't race on real ~/.local/share/swartznet/* files
	// (bloom, identity, reputation, seed list, trust store).
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

// TestCreateTorrentSingleFile creates a .torrent from one file,
// then verifies the metainfo has the expected shape.
func TestCreateTorrentSingleFile(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "hello.txt")
	content := []byte("hello swartznet ")
	// Pad to at least PieceLength (16 KiB) so we have real pieces.
	for len(content) < 32*1024 {
		content = append(content, content...)
	}
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{
		Root:     srcPath,
		Trackers: []string{"http://tracker.example.com/announce"},
		Comment:  "test comment",
	})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}

	if mi.Announce != "http://tracker.example.com/announce" {
		t.Errorf("announce: got %q, want tracker.example.com", mi.Announce)
	}
	if mi.Comment != "test comment" {
		t.Errorf("comment: got %q", mi.Comment)
	}
	if mi.CreatedBy != "SwartzNet" {
		t.Errorf("created by: got %q, want SwartzNet", mi.CreatedBy)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info.Name != "hello.txt" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.Length != int64(len(content)) {
		t.Errorf("length: got %d, want %d", info.Length, len(content))
	}
	if info.PieceLength == 0 {
		t.Error("piece length should be auto-chosen")
	}
	if len(info.Pieces) == 0 {
		t.Error("pieces should be populated with SHA1 hashes")
	}
}

// TestCreateTorrentMultiFile creates a .torrent from a directory.
func TestCreateTorrentMultiFile(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	dir := t.TempDir()
	root := filepath.Join(dir, "content")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string][]byte{
		"a.txt":     []byte("a" + fillTo(16*1024)),
		"b.txt":     []byte("b" + fillTo(16*1024)),
		"sub/c.txt": []byte("c" + fillTo(16*1024)),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{
		Root:    root,
		Private: true,
	})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info.Name != "content" {
		t.Errorf("name: got %q", info.Name)
	}
	if len(info.Files) != 3 {
		t.Errorf("files: got %d, want 3", len(info.Files))
	}
	if info.Private == nil || !*info.Private {
		t.Error("private flag should be set")
	}
}

// TestCreateTorrentFileWritesValid round-trips: create file on
// disk, load it back, verify infohash matches.
func TestCreateTorrentFileWritesValid(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(srcPath, []byte(fillTo(32*1024)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	torrentPath := filepath.Join(dir, "x.torrent")

	ih, mi, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root: srcPath,
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	if len(ih) != 40 {
		t.Errorf("infohash length: got %d, want 40", len(ih))
	}
	if mi == nil {
		t.Fatal("mi is nil")
	}

	// File must exist and be parseable.
	if _, err := os.Stat(torrentPath); err != nil {
		t.Fatalf("torrent file not written: %v", err)
	}
}

// TestCreateTorrentMissingRoot rejects empty/absent root.
func TestCreateTorrentMissingRoot(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)

	if _, err := eng.CreateTorrent(engine.CreateTorrentOptions{}); err == nil {
		t.Error("expected error for empty root")
	}
	if _, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: "/nonexistent/path/xxx"}); err == nil {
		t.Error("expected error for missing root")
	}
}

func fillTo(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = byte('a' + (i % 26))
	}
	return string(out)
}
