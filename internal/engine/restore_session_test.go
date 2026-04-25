package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// sessionEntryJSON mirrors the on-disk shape of internal/engine.sessionEntry.
// We keep an exact copy here so the test can hand-craft manifests
// without depending on internals — the JSON tags are part of the
// daemon's restore contract.
type sessionEntryJSON struct {
	InfoHash    string `json:"infohash"`
	AddedVia    string `json:"added_via"`
	MagnetURI   string `json:"magnet_uri,omitempty"`
	TorrentFile string `json:"torrent_file,omitempty"`
	Paused      bool   `json:"paused,omitempty"`
	Indexing    bool   `json:"indexing"`
	QueueOrder  int64  `json:"queue_order,omitempty"`
	SignedBy    string `json:"signed_by,omitempty"`
}

type sessionFileJSON struct {
	Version  int                `json:"version"`
	Torrents []sessionEntryJSON `json:"torrents"`
}

// writeSessionManifest writes a session.json under dataDir with
// the given entries, matching the format engine.loadSession reads.
func writeSessionManifest(t *testing.T, dataDir string, entries []sessionEntryJSON) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dataDir, "torrents"), 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(sessionFileJSON{Version: 1, Torrents: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "session.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func randomHex40(t *testing.T) string {
	t.Helper()
	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(ih[:])
}

// TestRestoreSessionMixedEntries hand-crafts a session manifest
// containing four entries that exercise four restoreEntry branches
// in one engine startup:
//   - bare-infohash entry (no magnet, no file): the default-case
//     metainfo.Hash.FromHexString path
//   - TorrentFile pointing to a file that doesn't exist on disk:
//     the os.ReadFile error path
//   - TorrentFile pointing to a non-bencode file: the metainfo.Load
//     error path
//   - bare-infohash entry with malformed hex: the FromHexString
//     error path
//
// All four error rows should be logged as warnings; the bare-
// infohash success row is restored successfully. RestoreSession
// must not return an error overall.
func TestRestoreSessionMixedEntries(t *testing.T) {
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

	goodIH := randomHex40(t)
	missingFileIH := randomHex40(t)
	corruptFileIH := randomHex40(t)

	// Plant a corrupt .torrent file under torrents/.
	torrentsDir := filepath.Join(dataDir, "torrents")
	if err := os.MkdirAll(torrentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	corruptTorrent := corruptFileIH + ".torrent"
	if err := os.WriteFile(filepath.Join(torrentsDir, corruptTorrent), []byte("not bencode"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeSessionManifest(t, dataDir, []sessionEntryJSON{
		{
			InfoHash: goodIH,
			AddedVia: "infohash",
			Indexing: true,
		},
		{
			InfoHash:    missingFileIH,
			AddedVia:    "file",
			TorrentFile: missingFileIH + ".torrent", // does not exist
			Indexing:    true,
		},
		{
			InfoHash:    corruptFileIH,
			AddedVia:    "file",
			TorrentFile: corruptTorrent, // exists but is garbage
			Indexing:    true,
		},
		{
			InfoHash: "this-is-not-hex",
			AddedVia: "infohash",
			Indexing: true,
		},
	})

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// daemon.New normally calls RestoreSession; engine_test calls it
	// directly to drive the loop in this package's tests too.
	if err := eng.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v (per-row failures must be tolerated)", err)
	}

	// Only the good bare-infohash entry should be live in the engine.
	if got := len(eng.Torrents()); got != 1 {
		t.Errorf("Torrents() = %d, want 1 (only the good bare-infohash entry should restore)", got)
	}
}
