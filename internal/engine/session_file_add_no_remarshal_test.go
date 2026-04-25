package engine_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestAddTorrentFileDoesNotRemarshalCopy is the regression
// gate for the upgradeMagnetSession race: AddTorrentFile copies
// the original .torrent bytes verbatim to torrents/<hex>.torrent.
// Before the fix landed, registerLocked spawned
// upgradeMagnetSession for *every* add; for file adds, that
// goroutine raced AddTorrentFile's writeTorrentCopy on the same
// path and could overwrite the saved bytes with a re-marshalled
// version that didn't byte-match the original — eventually
// breaking RestoreSession with "expected EOF" on metainfo.Load.
//
// The test reads the persisted .torrent and asserts a byte-exact
// match against the source. We sleep 500 ms after AddTorrentFile
// to give any rogue goroutine plenty of time to run; if the race
// regresses, the saved bytes will differ.
func TestAddTorrentFileDoesNotRemarshalCopy(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	src := filepath.Join(t.TempDir(), "fixture.torrent")
	ih := writeMinimalTorrent(t, src)
	original, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	if _, err := eng.AddTorrentFile(src); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	// Give any rogue goroutine generous time to run. With the
	// fix in place, upgradeMagnetSession is never spawned for
	// file adds; without the fix, it races and may overwrite
	// the saved .torrent within microseconds.
	time.Sleep(500 * time.Millisecond)

	saved, err := os.ReadFile(filepath.Join(dataDir, "torrents", ih+".torrent"))
	if err != nil {
		t.Fatalf("read saved torrent: %v", err)
	}

	if !bytes.Equal(original, saved) {
		t.Errorf("saved .torrent does not byte-match the original\n  original len=%d\n  saved    len=%d",
			len(original), len(saved))
	}
}
