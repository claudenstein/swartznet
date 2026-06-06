package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/swartznet/swartznet/internal/config"
)

// newPausedSkipEngine builds a fully local, DHT-less engine with all
// file-backed XDG paths disabled, suitable for queue/priority unit
// tests that must not touch the network or the user's state.
func newPausedSkipEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() {
		eng.Close()
		time.Sleep(20 * time.Millisecond)
	})
	return eng
}

// addLocalTorrentWithFiles creates a single-file torrent from bytes
// written under the engine's DataDir and adds it so its metadata
// (and therefore File list) is immediately available.
func addLocalTorrentWithFiles(t *testing.T, eng *Engine) *Handle {
	t.Helper()
	srcPath := filepath.Join(eng.cfg.DataDir, "paused.bin")
	if err := os.WriteFile(srcPath, make([]byte, 32*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(CreateTorrentOptions{Root: srcPath})
	if err != nil {
		t.Fatalf("CreateTorrent: %v", err)
	}
	hAny, err := eng.AddTorrentMetaInfo(mi)
	if err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v", err)
	}
	h, ok := hAny.(*Handle)
	if !ok {
		t.Fatalf("AddTorrentMetaInfo returned %T, want *Handle", hAny)
	}
	if h.T.Info() == nil {
		t.Fatal("expected metadata immediately after AddTorrentMetaInfo")
	}
	return h
}

// allFilesPriority returns the distinct priority labels across all
// files in the handle's torrent.
func filePriorities(h *Handle) []torrent.PiecePriority {
	out := make([]torrent.PiecePriority, 0, len(h.T.Files()))
	for _, f := range h.T.Files() {
		out = append(out, f.Priority())
	}
	return out
}

// TestActivateDownloadSkipsPausedHandle covers the paused guard added
// to activateDownload: a paused torrent must never have its files
// flipped to Normal priority, even when activation is requested
// directly (the path a restored-paused torrent's autoDownload
// goroutine takes once GotInfo is already closed).
func TestActivateDownloadSkipsPausedHandle(t *testing.T) {
	t.Parallel()
	eng := newPausedSkipEngine(t)
	h := addLocalTorrentWithFiles(t, eng)

	// Pause, then force every file back to None to simulate the
	// freshly-restored-paused state (priorities not yet flipped).
	h.pausedMu.Lock()
	h.paused = true
	h.pausedMu.Unlock()
	for _, f := range h.T.Files() {
		f.SetPriority(torrent.PiecePriorityNone)
	}

	activateDownload(h)

	for i, p := range filePriorities(h) {
		if p != torrent.PiecePriorityNone {
			t.Errorf("file %d priority = %v, want None (paused torrent must not be activated)", i, p)
		}
	}
}

// TestQueueOrActivateSkipsPausedHandle covers the paused guard added
// to queueOrActivate (the entry point autoDownload uses). A paused
// handle must not be activated nor marked queued.
func TestQueueOrActivateSkipsPausedHandle(t *testing.T) {
	t.Parallel()
	eng := newPausedSkipEngine(t)
	h := addLocalTorrentWithFiles(t, eng)

	h.pausedMu.Lock()
	h.paused = true
	h.pausedMu.Unlock()
	for _, f := range h.T.Files() {
		f.SetPriority(torrent.PiecePriorityNone)
	}

	eng.queueOrActivate(h)

	if h.IsQueued() {
		t.Error("queueOrActivate marked a paused handle queued; expected no-op")
	}
	for i, p := range filePriorities(h) {
		if p != torrent.PiecePriorityNone {
			t.Errorf("file %d priority = %v, want None", i, p)
		}
	}
}

// TestResumeReactivatesPausedTorrentPriorities verifies the Resume
// fix: a torrent paused before its priorities were flipped (files at
// None) gets them flipped back to Normal on Resume.
func TestResumeReactivatesPausedTorrentPriorities(t *testing.T) {
	t.Parallel()
	eng := newPausedSkipEngine(t)
	h := addLocalTorrentWithFiles(t, eng)
	hexIH := h.T.InfoHash().HexString()

	// Pause and reset files to None (the restored-paused shape).
	if err := eng.PauseTorrent(hexIH); err != nil {
		t.Fatalf("PauseTorrent: %v", err)
	}
	for _, f := range h.T.Files() {
		f.SetPriority(torrent.PiecePriorityNone)
	}

	if err := eng.ResumeTorrent(hexIH); err != nil {
		t.Fatalf("ResumeTorrent: %v", err)
	}

	// Resume re-runs activation in a goroutine; poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for {
		ok := true
		for _, p := range filePriorities(h) {
			if p != torrent.PiecePriorityNormal {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("files not flipped back to Normal after Resume: %v", filePriorities(h))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
