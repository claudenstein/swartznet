package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSaveLockedEmptyPathNoop covers the in-memory-only path:
// when s.path == "", saveLocked must return nil without writing.
func TestSaveLockedEmptyPathNoop(t *testing.T) {
	t.Parallel()
	s := &session{
		entries: map[string]sessionEntry{
			"abc": {InfoHash: "abc", AddedVia: "magnet"},
		},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveLocked(); err != nil {
		t.Errorf("saveLocked(no-path) err = %v, want nil", err)
	}
}

// TestSaveLockedSuccessSortsByQueueOrder covers the happy path
// and pins the documented sort contract: by QueueOrder ascending,
// ties broken by InfoHash.
func TestSaveLockedSuccessSortsByQueueOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	s := &session{
		path: path,
		entries: map[string]sessionEntry{
			"a": {InfoHash: "a", QueueOrder: 30},
			"b": {InfoHash: "b", QueueOrder: 10},
			"c": {InfoHash: "c", QueueOrder: 20},
			"d": {InfoHash: "d", QueueOrder: 10}, // ties b → InfoHash break
		},
	}
	s.mu.Lock()
	if err := s.saveLocked(); err != nil {
		t.Fatalf("saveLocked: %v", err)
	}
	s.mu.Unlock()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var out sessionFile
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Version != sessionFileVersion {
		t.Errorf("Version = %d, want %d", out.Version, sessionFileVersion)
	}
	wantOrder := []string{"b", "d", "c", "a"} // QueueOrder 10,10,20,30
	if len(out.Torrents) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(out.Torrents), len(wantOrder))
	}
	for i, want := range wantOrder {
		if out.Torrents[i].InfoHash != want {
			t.Errorf("Torrents[%d].InfoHash = %q, want %q", i, out.Torrents[i].InfoHash, want)
		}
	}
}

// TestSaveLockedWriteTempFails covers the os.WriteFile error
// branch — plant a non-empty directory at <path>.tmp so the
// truncate-open in os.WriteFile fails.
func TestSaveLockedWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	tmp := path + ".tmp"
	// Plant a non-empty directory at the tempfile path.
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &session{path: path, entries: map[string]sessionEntry{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveLocked(); err == nil {
		t.Error("expected WriteFile error when tmp is a directory")
	}
}

// TestSaveLockedRenameFails covers the os.Rename error branch —
// plant a non-empty directory at the final path so Rename refuses
// to overwrite. Asserts the tempfile is cleaned up afterwards.
func TestSaveLockedRenameFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("rename-onto-non-empty-dir semantics differ on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &session{path: path, entries: map[string]sessionEntry{}}
	s.mu.Lock()
	err := s.saveLocked()
	s.mu.Unlock()
	if err == nil {
		t.Error("expected Rename error")
	}
	if _, statErr := os.Stat(path + ".tmp"); !os.IsNotExist(statErr) {
		t.Errorf("tempfile leaked: stat err = %v", statErr)
	}
}
