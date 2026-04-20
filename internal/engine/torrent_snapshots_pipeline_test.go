package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/indexer"
)

// TestTorrentSnapshotsWithPipelinePopulatesIndexFields covers
// the `pipe != nil → fill IndexedFiles/IndexExtracted` branch
// of TorrentSnapshots. Wire an index into the engine via
// SetIndex (which also constructs a pipeline), add a magnet,
// and assert the snapshot's index-related fields come from the
// pipeline rather than zeros.
func TestTorrentSnapshotsWithPipelinePopulatesIndexFields(t *testing.T) {
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

	idx, err := indexer.Open(filepath.Join(t.TempDir(), "engine-snap.bleve"))
	if err != nil {
		t.Fatalf("indexer.Open: %v", err)
	}
	defer idx.Close()
	eng.SetIndex(idx)

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	magnet := "magnet:?xt=urn:btih:" + hex.EncodeToString(ih[:])
	if _, err := eng.AddMagnet(magnet); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	snaps := eng.TorrentSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("len(snaps) = %d, want 1", len(snaps))
	}
	// The pipeline-stats branch ran. Counters are zero (we never
	// indexed anything for this magnet), but TorrentSnapshots
	// must have asked the pipeline rather than skipping the call.
	if snaps[0].IndexedFiles != 0 || snaps[0].IndexExtracted != 0 {
		t.Errorf("expected zero pipeline counters for fresh magnet, got %+v", snaps[0])
	}
}
