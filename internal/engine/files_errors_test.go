package engine_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// newFilesErrorEngine spins up a DHT-disabled, no-upload engine
// with file-backed paths cleared. Mirrors the helper pattern from
// other engine tests.
func newFilesErrorEngine(t *testing.T) (*engine.Engine, func()) {
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

// TestTorrentFilesPreMetadataReturnsEmpty covers the t.Info()==nil
// branch in TorrentFiles. A magnet-added torrent without metadata
// returns an empty slice (not an error).
func TestTorrentFilesPreMetadataReturnsEmpty(t *testing.T) {
	t.Parallel()
	eng, cleanup := newFilesErrorEngine(t)
	defer cleanup()

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	hexIH := hex.EncodeToString(ih[:])
	if _, err := eng.AddMagnet("magnet:?xt=urn:btih:" + hexIH); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	files, err := eng.TorrentFiles(hexIH)
	if err != nil {
		t.Fatalf("TorrentFiles: %v", err)
	}
	if files == nil {
		t.Error("pre-metadata TorrentFiles should return empty slice, not nil")
	}
	if len(files) != 0 {
		t.Errorf("pre-metadata files len = %d, want 0", len(files))
	}
}

// TestSetFilePriorityPreMetadataErrors covers the
// "torrent metadata not yet available" branch.
func TestSetFilePriorityPreMetadataErrors(t *testing.T) {
	t.Parallel()
	eng, cleanup := newFilesErrorEngine(t)
	defer cleanup()

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	hexIH := hex.EncodeToString(ih[:])
	if _, err := eng.AddMagnet("magnet:?xt=urn:btih:" + hexIH); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	if err := eng.SetFilePriority(hexIH, 0, engine.FilePriorityNormal); err == nil {
		t.Error("SetFilePriority before metadata arrives should error")
	}
}

// TestSetFilePriorityOutOfRangeAndUnknownValue exercises:
//   - the file-index range check
//   - the toAnacrolix unknown-priority branch
// We use a real single-file torrent so the metadata gate passes.
func TestSetFilePriorityOutOfRangeAndUnknownValue(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t) // from create_test.go: real engine + cleanup

	dir := t.TempDir()
	srcPath := dir + "/file.bin"
	if err := writeBinaryForTorrent(srcPath); err != nil {
		t.Fatal(err)
	}
	torrentPath := dir + "/x.torrent"
	ih, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: srcPath}, torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.AddTorrentFile(torrentPath); err != nil {
		t.Fatal(err)
	}

	// Out-of-range index.
	if err := eng.SetFilePriority(ih, 999, engine.FilePriorityNormal); err == nil {
		t.Error("SetFilePriority with out-of-range index should error")
	}
	// Unknown priority value (toAnacrolix returns the wrapped error).
	if err := eng.SetFilePriority(ih, 0, engine.FilePriority("bogus-prio")); err == nil {
		t.Error("SetFilePriority with unknown priority should error")
	}
}

// writeBinaryForTorrent writes a 32 KiB file at path so the
// CreateTorrent call has real content to hash.
func writeBinaryForTorrent(path string) error {
	data := make([]byte, 32*1024)
	for i := range data {
		data[i] = byte('A' + i%26)
	}
	return os.WriteFile(path, data, 0o644)
}

// satisfy io import if otherwise unused.
var _ io.Writer = (*os.File)(nil)
