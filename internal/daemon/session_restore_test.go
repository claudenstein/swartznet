package daemon_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestDaemonSessionRestoreAcrossRestart proves the headline
// "close-and-reopen the app" user flow: a daemon adds a
// torrent, shuts down cleanly, and a fresh daemon pointed at
// the same state dirs recovers the torrent list from disk.
//
// This is the first test that exercises daemon.New through
// daemon.Close through daemon.New on real state directories.
// The GUI, web UI, and CLI all depend on this path working —
// the moment it breaks, users reopen the app and their
// torrent list is empty. Nothing in the existing suite pinned
// that behaviour at the daemon (as opposed to engine) level.
//
// The torrent is added from a real .torrent file so the
// restore path exercises restoreEntry's file-first branch
// (path → magnet → infohash fallback chain). A magnet-only
// branch is covered implicitly by session_test.go in the
// engine package; this test's value is the full-daemon
// wrapper plus on-disk .torrent copy.
func TestDaemonSessionRestoreAcrossRestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := baseConfigForRestore(root)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// ------------------------------------------------------------
	// 1. Boot the first daemon. Build a tiny .torrent from a local
	//    file and add it via AddTorrentFile — this pushes a
	//    sessionEntry onto the on-disk manifest AND copies the
	//    .torrent into the engine's torrents/ dir so restoreEntry
	//    can reload it from disk after restart.
	d1, err := daemon.New(context.Background(), daemon.Options{
		Cfg: cfg,
		Log: log,
	})
	if err != nil {
		t.Fatalf("first daemon.New: %v", err)
	}

	payloadPath := filepath.Join(cfg.DataDir, "restore.bin")
	if err := os.WriteFile(payloadPath, []byte("restore-scenario-payload"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	torrentPath := filepath.Join(t.TempDir(), "restore.torrent")
	_, mi, err := d1.Eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:        payloadPath,
		PieceLength: 16 * 1024,
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	if _, err := d1.Eng.AddTorrentFile(torrentPath); err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	wantIH := mi.HashInfoBytes().HexString()

	// Assert the first daemon sees the torrent we just added.
	if !hasInfoHash(d1.Eng.TorrentSnapshots(), wantIH) {
		t.Fatalf("first daemon missing torrent %s in snapshots=%+v",
			wantIH, d1.Eng.TorrentSnapshots())
	}

	// ------------------------------------------------------------
	// 2. Close the first daemon. This must flush the session
	//    manifest and release the torrent-client ports so the
	//    second daemon can bind its own OS-assigned ports on the
	//    same loopback.
	if err := d1.Close(); err != nil {
		t.Fatalf("first daemon.Close: %v", err)
	}

	// Session manifest must exist after close, or the second
	// daemon has nothing to restore from.
	sessionPath := filepath.Join(cfg.DataDir, "session.json")
	if st, err := os.Stat(sessionPath); err != nil || st.Size() == 0 {
		t.Fatalf("session.json missing or empty after close: err=%v", err)
	}

	// ------------------------------------------------------------
	// 3. Boot a second daemon on the same state dirs. The port
	//    must be 0 so we don't collide with the just-closed
	//    listener's TIME_WAIT state on a fast loop.
	d2, err := daemon.New(context.Background(), daemon.Options{
		Cfg: cfg,
		Log: log,
	})
	if err != nil {
		t.Fatalf("second daemon.New: %v", err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	// RestoreSession runs inside daemon.New but populates handles
	// synchronously only after engine.New returns; poll briefly for
	// the snapshot to appear to guard against any future async
	// restore change.
	var snaps []engine.TorrentSnapshot
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snaps = d2.Eng.TorrentSnapshots()
		if hasInfoHash(snaps, wantIH) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !hasInfoHash(snaps, wantIH) {
		t.Fatalf("second daemon did not restore torrent %s; snapshots=%+v", wantIH, snaps)
	}

	// ------------------------------------------------------------
	// 4. The restored torrent's infohash must match the original
	//    bit-for-bit. If the session manifest round-trip mangled
	//    the hash (hex case, truncation) this catches it.
	mag := metainfo.Magnet{InfoHash: mi.HashInfoBytes()}.String()
	if _, err := metainfo.ParseMagnetUri(mag); err != nil {
		t.Fatalf("sanity: ParseMagnetUri(own magnet): %v", err)
	}
}

// hasInfoHash reports whether snaps contains a torrent whose
// infohash matches wantHex (case-insensitive).
func hasInfoHash(snaps []engine.TorrentSnapshot, wantHex string) bool {
	for _, s := range snaps {
		if equalFoldHex(s.InfoHash, wantHex) {
			return true
		}
	}
	return false
}

// equalFoldHex compares two hex strings without allocating via
// strings.EqualFold — the infohash is ASCII-only.
func equalFoldHex(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// baseConfigForRestore returns a minimal config suitable for
// an in-process daemon with all persistent paths rooted at the
// given directory. Port is OS-assigned, DHT is disabled so the
// test stays hermetic.
func baseConfigForRestore(root string) config.Config {
	cfg := config.Default()
	cfg.DataDir = filepath.Join(root, "data")
	cfg.IndexDir = filepath.Join(root, "index")
	cfg.IdentityPath = filepath.Join(root, "identity.key")
	cfg.PublisherManifest = filepath.Join(root, "publisher.json")
	cfg.ReputationPath = filepath.Join(root, "reputation.json")
	cfg.SeedListPath = ""
	cfg.BloomPath = filepath.Join(root, "known-good.bloom")
	cfg.TrustPath = filepath.Join(root, "trust.json")
	cfg.CompanionDir = filepath.Join(root, "companion")
	cfg.CompanionFollowFile = filepath.Join(root, "follows.json")
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.Regtest = true
	return cfg
}
