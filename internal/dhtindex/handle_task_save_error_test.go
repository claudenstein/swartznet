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

// TestHandleTaskSaveErrorIsLogged covers handleTask's
// `manifest.Save() != nil → Warn` branch — bind manifest to a
// real path, plant a non-empty directory at the path so Save's
// rename fails, then call handleTask directly with a valid task.
// The Save error must be swallowed (logged) rather than
// propagated, so handleTask returns normally.
//
// Skipped on Windows because of differing rename-onto-non-empty-
// dir semantics.
func TestHandleTaskSaveErrorIsLogged(t *testing.T) {
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

	// Plant a non-empty directory at the manifest path so Save
	// fails on rename.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	put := &recordingPutter{}
	p := NewPublisher(put, mf, PublisherOptions{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// A valid task — Tokenize returns at least one keyword.
	p.handleTask(context.Background(), PublishTask{
		InfoHash: bytes.Repeat([]byte{0xab}, 20),
		Name:     "Ubuntu 24.04 Desktop",
	})

	// publishOne must have fired at least once before Save failed.
	if put.calls.Load() == 0 {
		t.Errorf("Put never fired; expected at least one")
	}
}
