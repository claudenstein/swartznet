package engine_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestRestoreSessionBadHexInfoHash covers restoreEntry's
// `ih.FromHexString(entry.InfoHash) err → break` branch in the
// default (bare-infohash) case. The existing mixed-entries test
// uses a too-short InfoHash that loadSession's `len != 40`
// filter drops before restoreEntry sees it; we need a 40-char
// non-hex string to actually reach restoreEntry's parser.
func TestRestoreSessionBadHexInfoHash(t *testing.T) {
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

	// 40 'z's: passes loadSession's length filter but fails the
	// hex.DecodeString in metainfo.Hash.FromHexString.
	badHex := strings.Repeat("z", 40)
	writeSessionManifest(t, dataDir, []sessionEntryJSON{
		{
			InfoHash: badHex,
			AddedVia: "infohash",
			Indexing: true,
		},
	})

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()

	// RestoreSession tolerates per-entry failures; overall must succeed.
	if err := eng.RestoreSession(); err != nil {
		t.Fatalf("RestoreSession: %v (per-row failure must be tolerated)", err)
	}
	// The bad-hex entry must NOT have been added.
	if got := len(eng.Torrents()); got != 0 {
		t.Errorf("Torrents() = %d, want 0 (bad-hex entry should be skipped)", got)
	}
}
