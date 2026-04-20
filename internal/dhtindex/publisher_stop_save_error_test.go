package dhtindex_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestPublisherStopSaveError covers Publisher.Stop's
// `manifest.Save() != nil → Warn` branch. Bind the manifest to
// a real path so Save is non-trivial, then plant a non-empty
// directory at that path so Save can't rename onto it; Stop
// must still complete (the error is swallowed and logged).
//
// Skipped on Windows because of differing rename-onto-non-empty-
// dir semantics.
func TestPublisherStopSaveError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("rename-onto-non-empty-dir semantics differ on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	mf, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	p := dhtindex.NewPublisher(&failingPutter{err: errors.New("x")}, mf,
		dhtindex.DefaultPublisherOptions(), silentLogger())
	p.Start()

	// Plant a non-empty directory at the manifest path so the
	// final Save() inside Stop fails. The publisher swallows
	// and logs the error; Stop must still return.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	p.Stop() // must not panic / block; Save error is logged.
}
