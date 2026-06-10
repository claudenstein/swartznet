package engine_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestRestoreSessionRejectsUnsafeTorrentFileName covers the
// plain-file-name guard added to restoreEntry: a TorrentFile value
// carrying path separators or ".." must be rejected instead of being
// joined into torrentsDir, so a corrupted or hand-edited manifest
// cannot make the engine read .torrent files outside the torrents
// dir. A real, loadable .torrent is planted at the traversal target
// (DataDir/evil.torrent, one level above torrents/) — the old code
// happily restored it.
func TestRestoreSessionRejectsUnsafeTorrentFileName(t *testing.T) {
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

	// Manifest rows exercising the separator, ".." and nested-path
	// shapes. Written before engine.New so loadSession picks them up.
	writeSessionManifest(t, dataDir, []sessionEntryJSON{
		{InfoHash: randomHex40(t), AddedVia: "file", TorrentFile: "../evil.torrent", Indexing: true},
		{InfoHash: randomHex40(t), AddedVia: "file", TorrentFile: "..", Indexing: true},
		{InfoHash: randomHex40(t), AddedVia: "file", TorrentFile: "sub/evil.torrent", Indexing: true},
	})

	// Plant a valid .torrent one level above torrents/ — exactly
	// where "../evil.torrent" resolves.
	info := metainfo.Info{
		Name:        "evil.bin",
		PieceLength: 16384,
		Length:      4,
		Pieces:      make([]byte, 20),
	}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	var buf bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: ib}).Write(&buf); err != nil {
		t.Fatalf("metainfo.Write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "evil.torrent"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// Per-entry failures are tolerated; overall restore must succeed.
	if err := eng.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v", err)
	}
	// None of the unsafe rows may have been restored — in particular
	// the planted ../evil.torrent must NOT have been loaded.
	if got := len(eng.Torrents()); got != 0 {
		t.Errorf("Torrents() = %d, want 0 (unsafe TorrentFile rows must be skipped)", got)
	}
}
