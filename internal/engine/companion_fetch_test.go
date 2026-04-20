package engine_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// newCompanionTestEngine spins up the same DHT-disabled, no-upload
// engine the other tests use, with the file-backed paths cleared
// so we don't accidentally inherit user-level XDG state.
func newCompanionTestEngine(t *testing.T) (*engine.Engine, func()) {
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

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng, func() { _ = eng.Close() }
}

// TestFetchCompanionTorrentCancelledContext exercises the early-
// cancellation branch: AddInfoHash succeeds, then the select on
// GotInfo / ctx.Done picks ctx.Done because we pre-cancelled. The
// function returns ctx.Err() without touching the file system.
func TestFetchCompanionTorrentCancelledContext(t *testing.T) {
	t.Parallel()
	eng, cleanup := newCompanionTestEngine(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call

	var ih [20]byte
	ih[0] = 0xab

	path, err := eng.FetchCompanionTorrent(ctx, ih)
	if err == nil {
		t.Fatal("FetchCompanionTorrent should error on a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
}

func TestAddTorrentMetaInfoNil(t *testing.T) {
	t.Parallel()
	eng, cleanup := newCompanionTestEngine(t)
	defer cleanup()

	if _, err := eng.AddTorrentMetaInfo(nil); err == nil {
		t.Error("AddTorrentMetaInfo(nil) should error")
	}
}

// TestAddInfoHashAfterCloseFails covers the closed-engine guard in
// AddInfoHash. Re-using the cleanup pattern would close-then-reuse,
// so we close manually and skip the deferred cleanup.
func TestAddInfoHashAfterCloseFails(t *testing.T) {
	t.Parallel()
	eng, _ := newCompanionTestEngine(t)
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var ih [20]byte
	ih[0] = 0xcd
	if _, err := eng.AddInfoHash(ih); err == nil {
		t.Error("AddInfoHash after Close should error")
	}
}

func TestAddTorrentMetaInfoAfterCloseFails(t *testing.T) {
	t.Parallel()
	eng, _ := newCompanionTestEngine(t)
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Pass a syntactically-valid metainfo so the nil-guard does
	// not short-circuit; the closed-engine guard is what we want to
	// hit. The info dict only needs to bencode-marshal cleanly —
	// we never actually start downloading.
	info := metainfo.Info{
		Name:        "closed-engine-fixture",
		Length:      0,
		PieceLength: 16384,
		Pieces:      []byte{},
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes}

	if _, err := eng.AddTorrentMetaInfo(mi); err == nil {
		t.Error("AddTorrentMetaInfo after Close should error")
	}
}
