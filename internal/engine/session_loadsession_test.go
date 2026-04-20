package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadSessionEmptyDataDir covers the dataDir == "" early
// return: an in-memory-only session with no path and no
// torrentsDir.
func TestLoadSessionEmptyDataDir(t *testing.T) {
	t.Parallel()
	s, err := loadSession("")
	if err != nil {
		t.Fatalf("loadSession(\"\"): %v", err)
	}
	if s == nil || s.entries == nil || s.path != "" || s.torrentsDir != "" {
		t.Errorf("got %+v, want in-memory-only session", s)
	}
}

// TestLoadSessionNoFileYetReturnsEmpty covers the os.ErrNotExist
// branch: a fresh dataDir with no session.json must return an
// empty session, not an error.
func TestLoadSessionNoFileYetReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := loadSession(dir)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if len(s.entries) != 0 {
		t.Errorf("entries len = %d, want 0", len(s.entries))
	}
	if s.path != filepath.Join(dir, "session.json") {
		t.Errorf("path = %q", s.path)
	}
}

// TestLoadSessionEmptyFileReturnsEmpty covers the len(raw) == 0
// branch: a zero-byte session.json must produce an empty session
// rather than a JSON-decode error.
func TestLoadSessionEmptyFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadSession(dir)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if len(s.entries) != 0 {
		t.Errorf("entries len = %d, want 0", len(s.entries))
	}
}

// TestLoadSessionGarbageJSONErrors covers the json.Unmarshal
// error branch: garbage bytes in session.json must return a
// wrapped "decode session" error rather than silently dropping
// state.
func TestLoadSessionGarbageJSONErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadSession(dir)
	if err == nil {
		t.Fatal("expected JSON decode error")
	}
	if !strings.Contains(err.Error(), "decode session") {
		t.Errorf("err = %q, want it to wrap 'decode session'", err.Error())
	}
}

// TestLoadSessionShortInfoHashSkipped covers the
// `len(InfoHash) != 40` filter — an entry whose hex infohash is
// the wrong length must NOT be loaded into the entries map.
func TestLoadSessionShortInfoHashSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := strings.Repeat("a", 40)
	bad := strings.Repeat("b", 39)
	body := `{"version":1,"torrents":[
		{"infohash":"` + good + `","added_via":"magnet"},
		{"infohash":"` + bad + `","added_via":"magnet"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadSession(dir)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if _, ok := s.entries[good]; !ok {
		t.Errorf("good entry missing")
	}
	if _, ok := s.entries[bad]; ok {
		t.Errorf("short-infohash entry should have been filtered out")
	}
}
