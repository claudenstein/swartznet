package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestSessionRoundTrip is the canonical "torrents survive a restart"
// test. Adds two torrents (one magnet, one .torrent file), tweaks
// per-torrent state, closes the engine, opens a fresh engine on the
// same DataDir, and verifies both torrents come back with the saved
// state intact.
func TestSessionRoundTrip(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	// Hermetic: zero out the user-level XDG paths so parallel
	// tests don't race on the real ~/.local/share/swartznet/
	// bloom/identity/etc. files.
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First lifecycle: add two torrents, tweak state.
	eng1, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New (#1): %v", err)
	}

	// Magnet-style add (no metadata fetch in this isolated test —
	// we just want the infohash on the engine's books).
	magnetIH := randomInfoHash(t)
	magnetHex := hex.EncodeToString(magnetIH[:])
	magnet := "magnet:?xt=urn:btih:" + magnetHex
	if _, err := eng1.AddMagnet(magnet); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	// .torrent file add — synthesize a tiny single-file metainfo.
	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	fileIH := writeMinimalTorrent(t, torrentPath)
	if _, err := eng1.AddTorrentFile(torrentPath); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	// Pause the magnet, disable indexing on the .torrent.
	if err := eng1.PauseTorrent(magnetHex); err != nil {
		t.Fatalf("PauseTorrent: %v", err)
	}
	if err := eng1.SetTorrentIndexing(fileIH, false); err != nil {
		t.Fatalf("SetTorrentIndexing: %v", err)
	}

	if got := len(eng1.Torrents()); got != 2 {
		t.Fatalf("eng1 Torrents len = %d, want 2", got)
	}
	if err := eng1.Close(); err != nil {
		t.Fatalf("eng1 Close: %v", err)
	}

	// Verify the on-disk artefacts. The magnet entry should not
	// have a torrents/<hex>.torrent yet (no metadata arrived); the
	// file entry MUST.
	if _, err := os.Stat(filepath.Join(dataDir, "session.json")); err != nil {
		t.Fatalf("session.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "torrents", fileIH+".torrent")); err != nil {
		t.Fatalf("torrents/%s.torrent missing: %v", fileIH, err)
	}

	// Second lifecycle: fresh engine, restore.
	eng2, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New (#2): %v", err)
	}
	defer eng2.Close()
	if err := eng2.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}

	snaps := eng2.TorrentSnapshots()
	if len(snaps) != 2 {
		t.Fatalf("eng2 snapshots len = %d, want 2: %+v", len(snaps), snaps)
	}
	byHash := map[string]engine.TorrentSnapshot{}
	for _, s := range snaps {
		byHash[s.InfoHash] = s
	}
	mag, ok := byHash[magnetHex]
	if !ok {
		t.Fatalf("magnet torrent %s not restored; got hashes: %v", magnetHex, snapshotHashes(snaps))
	}
	if !mag.Paused {
		t.Errorf("magnet torrent expected Paused=true, got %+v", mag)
	}
	fil, ok := byHash[fileIH]
	if !ok {
		t.Fatalf("file torrent %s not restored", fileIH)
	}
	if fil.Indexing {
		t.Errorf("file torrent expected Indexing=false, got %+v", fil)
	}
}

// TestSessionRemoveDeletesCopy verifies that RemoveTorrent drops the
// session entry AND the torrents/<hex>.torrent file copy.
func TestSessionRemoveDeletesCopy(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	// Hermetic: zero out the user-level XDG paths.
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	eng, err := engine.New(ctx, cfg, log)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	ih := writeMinimalTorrent(t, torrentPath)
	if _, err := eng.AddTorrentFile(torrentPath); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}
	copyPath := filepath.Join(dataDir, "torrents", ih+".torrent")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("torrent copy missing pre-remove: %v", err)
	}
	if err := eng.RemoveTorrent(ih); err != nil {
		t.Fatalf("RemoveTorrent: %v", err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Errorf("torrent copy survived removal: stat err = %v", err)
	}
}

func snapshotHashes(snaps []engine.TorrentSnapshot) []string {
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.InfoHash)
	}
	return out
}

// randomInfoHash returns 20 random bytes — used to mint magnet URIs
// that won't collide with the synthesised .torrent file in the same
// test. Returns a [20]byte for direct use as a metainfo.Hash.
func randomInfoHash(t *testing.T) [20]byte {
	t.Helper()
	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return ih
}

// writeMinimalTorrent serialises the smallest valid metainfo we can
// produce — a single empty file with one zero-length piece — to path,
// and returns its 40-char hex infohash.
func writeMinimalTorrent(t *testing.T, path string) string {
	t.Helper()
	info := metainfo.Info{
		Name:        "session-test-fixture",
		Length:      0,
		PieceLength: 16384,
		Pieces:      []byte{}, // zero-length: matches the zero-length file
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
		CreatedBy: "session_test",
	}
	miBytes, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("marshal mi: %v", err)
	}
	if err := os.WriteFile(path, miBytes, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	ih := metainfo.HashBytes(infoBytes)
	return ih.HexString()
}
