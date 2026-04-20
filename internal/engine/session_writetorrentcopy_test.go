package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWriteTorrentCopyNoTorrentsDir covers the
// "in-memory-only mode" early return — when torrentsDir is "",
// writeTorrentCopy must return ("", nil) without touching disk.
func TestWriteTorrentCopyNoTorrentsDir(t *testing.T) {
	t.Parallel()
	s := &session{torrentsDir: ""}
	name, err := s.writeTorrentCopy("aabbccddee", []byte("payload"))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "" {
		t.Errorf("name = %q, want \"\"", name)
	}
}

// TestWriteTorrentCopyNilSession covers the s == nil guard.
func TestWriteTorrentCopyNilSession(t *testing.T) {
	t.Parallel()
	var s *session
	name, err := s.writeTorrentCopy("aabbccddee", []byte("payload"))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if name != "" {
		t.Errorf("name = %q, want \"\"", name)
	}
}

// TestWriteTorrentCopySuccess covers the happy path: the bytes
// land at <torrentsDir>/<infohash>.torrent and the returned name
// matches.
func TestWriteTorrentCopySuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &session{torrentsDir: dir}
	want := []byte("synthetic-bencode-bytes")
	name, err := s.writeTorrentCopy("0123456789abcdef0123", want)
	if err != nil {
		t.Fatalf("writeTorrentCopy: %v", err)
	}
	if name != "0123456789abcdef0123.torrent" {
		t.Errorf("name = %q, want \"0123456789abcdef0123.torrent\"", name)
	}
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("contents = %q, want %q", got, want)
	}
}

// TestWriteTorrentCopyWriteFails covers the os.WriteFile error
// branch — torrentsDir doesn't exist, so the tempfile create fails.
func TestWriteTorrentCopyWriteFails(t *testing.T) {
	t.Parallel()
	s := &session{torrentsDir: filepath.Join(t.TempDir(), "no-such-subdir")}
	name, err := s.writeTorrentCopy("aabbccddee", []byte("payload"))
	if err == nil {
		t.Errorf("expected WriteFile error, got name=%q", name)
	}
}

// TestWriteTorrentCopyRenameFails covers the os.Rename error
// branch — plant a non-empty directory at the target path so
// rename refuses to overwrite it. Also asserts the tempfile is
// cleaned up on the failure path.
func TestWriteTorrentCopyRenameFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("rename-onto-non-empty-dir semantics differ on Windows")
	}
	dir := t.TempDir()
	s := &session{torrentsDir: dir}
	hexIH := "deadbeefdeadbeefdead"
	target := filepath.Join(dir, hexIH+".torrent")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := s.writeTorrentCopy(hexIH, []byte("x")); err == nil {
		t.Error("expected rename error, got nil")
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile leaked: stat err = %v", err)
	}
}
