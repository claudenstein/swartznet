package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/swartznet/swartznet/internal/config"
)

// minimalTorrentFile produces the smallest valid single-file
// .torrent we can — one zero-length file and one zero-length
// piece — and returns the path + 40-char hex infohash.
func minimalTorrentFile(t *testing.T, dir string) (string, string) {
	t.Helper()
	info := metainfo.Info{
		Name:        "sample-rate-fixture",
		Length:      0,
		PieceLength: 16384,
		Pieces:      []byte{},
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, CreatedBy: "sample_rate_test"}
	miBytes, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.torrent")
	if err := os.WriteFile(path, miBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, metainfo.HashBytes(infoBytes).HexString()
}

// TestSampleRateFirstCallSeedsZeros pins the documented first-
// call behaviour: sampleRate seeds its internal state and
// returns (0, 0).
func TestSampleRateFirstCallSeedsZeros(t *testing.T) {
	t.Parallel()
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
		t.Fatal(err)
	}
	defer eng.Close()

	path, ihHex := minimalTorrentFile(t, t.TempDir())
	if _, err := eng.AddTorrentFile(path); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}
	hash, _ := metainfoHashFromHex(ihHex)
	var ih [20]byte
	copy(ih[:], hash[:])
	h, err := eng.HandleByInfoHash(ih)
	if err != nil {
		t.Fatal(err)
	}

	dr, ur := h.sampleRate()
	if dr != 0 || ur != 0 {
		t.Errorf("first sampleRate = (%d, %d), want (0, 0)", dr, ur)
	}
}

// TestSampleRateSubsequentSubMillisecondReturnsCached covers the
// elapsed < 100ms branch: a tight back-to-back call returns the
// cached rates.
func TestSampleRateSubsequentSubMillisecondReturnsCached(t *testing.T) {
	t.Parallel()
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
		t.Fatal(err)
	}
	defer eng.Close()

	path, ihHex := minimalTorrentFile(t, t.TempDir())
	if _, err := eng.AddTorrentFile(path); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}
	hash, _ := metainfoHashFromHex(ihHex)
	var ih [20]byte
	copy(ih[:], hash[:])
	h, err := eng.HandleByInfoHash(ih)
	if err != nil {
		t.Fatal(err)
	}

	// Seed.
	_, _ = h.sampleRate()
	// Tight follow-up; expected to hit elapsed < 100ms branch.
	dr, ur := h.sampleRate()
	if dr != 0 || ur != 0 {
		// On a zero-length torrent the cached rates are zero;
		// the branch ran cleanly.
		t.Errorf("second sampleRate = (%d, %d), want (0, 0)", dr, ur)
	}

	// And a third call after sleeping past the 100ms gate
	// should still produce non-negative numbers.
	time.Sleep(120 * time.Millisecond)
	dr, ur = h.sampleRate()
	if dr < 0 || ur < 0 {
		t.Errorf("third sampleRate negative: (%d, %d)", dr, ur)
	}
}

// metainfoHashFromHex converts a 40-char lowercase hex string to
// a metainfo.Hash. Local helper so the test stays self-contained.
func metainfoHashFromHex(s string) (metainfo.Hash, error) {
	var h metainfo.Hash
	err := h.FromHexString(s)
	return h, err
}
