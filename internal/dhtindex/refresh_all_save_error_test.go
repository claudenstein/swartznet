package dhtindex

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRefreshAllSaveErrorIsLogged covers refreshAll's
// `manifest.Save() != nil → Warn` branch — bind manifest to a
// real path, plant a non-empty directory at the path so Save's
// rename fails, and call refreshAll directly. The Save error
// must be swallowed (logged) rather than propagated, so
// refreshAll still returns normally.
//
// Skipped on Windows because of differing rename-onto-non-empty-
// dir semantics.
func TestRefreshAllSaveErrorIsLogged(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("rename-onto-non-empty-dir semantics differ on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	mf, err := LoadOrCreateManifest(path)
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	if _, err := mf.AddHit("ubuntu", KeywordHit{IH: bytes.Repeat([]byte{1}, 20), N: "u"}); err != nil {
		t.Fatal(err)
	}

	// Plant a non-empty directory at the manifest path so Save
	// fails on rename (the *.tmp write itself succeeds because
	// only the final path is blocked).
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Must not panic / propagate the Save failure.
	p.refreshAll(context.Background())

	if got := put.calls.Load(); got != 1 {
		t.Errorf("Put calls = %d, want 1 (publishOne still ran before Save failed)", got)
	}
}
