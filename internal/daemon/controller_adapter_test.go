package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// newAdapterEngine spins up an engine with all networking disabled,
// mirroring daemon_test.go's setup. The returned cleanup must be
// deferred even on test failure.
func newAdapterEngine(t *testing.T) (*engine.Engine, func()) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.IndexDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng, func() { _ = eng.Close() }
}

func TestControllerAdapterEmptyState(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAdapterEngine(t)
	defer cleanup()
	c := &controllerAdapter{eng: eng}

	if snaps := c.TorrentSnapshots(); len(snaps) != 0 {
		t.Errorf("TorrentSnapshots on empty engine = %d, want 0", len(snaps))
	}
}

func TestControllerAdapterRateLimitsRoundTrip(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAdapterEngine(t)
	defer cleanup()
	c := &controllerAdapter{eng: eng}

	c.SetUploadLimitBytesPerSec(123_456)
	if got := c.UploadLimitBytesPerSec(); got != 123_456 {
		t.Errorf("UploadLimitBytesPerSec = %d, want 123456", got)
	}

	c.SetDownloadLimitBytesPerSec(987_654)
	if got := c.DownloadLimitBytesPerSec(); got != 987_654 {
		t.Errorf("DownloadLimitBytesPerSec = %d, want 987654", got)
	}

	// Zero disables the limit; the engine reports 0 back.
	c.SetUploadLimitBytesPerSec(0)
	if got := c.UploadLimitBytesPerSec(); got != 0 {
		t.Errorf("UploadLimitBytesPerSec after disable = %d, want 0", got)
	}
}

func TestControllerAdapterMaxActiveDownloadsRoundTrip(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAdapterEngine(t)
	defer cleanup()
	c := &controllerAdapter{eng: eng}

	c.SetMaxActiveDownloads(7)
	if got := c.MaxActiveDownloads(); got != 7 {
		t.Errorf("MaxActiveDownloads = %d, want 7", got)
	}
}

func TestControllerAdapterAddMagnetURIRejectsGarbage(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAdapterEngine(t)
	defer cleanup()
	c := &controllerAdapter{eng: eng}

	if _, err := c.AddMagnetURI("not-a-magnet"); err == nil {
		t.Error("AddMagnetURI should reject non-magnet URI")
	}
}

func TestControllerAdapterUnknownInfoHash(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAdapterEngine(t)
	defer cleanup()
	c := &controllerAdapter{eng: eng}

	const bogus = "0000000000000000000000000000000000000000"

	if err := c.PauseTorrent(bogus); err == nil {
		t.Error("PauseTorrent unknown infohash should error")
	}
	if err := c.ResumeTorrent(bogus); err == nil {
		t.Error("ResumeTorrent unknown infohash should error")
	}
	if err := c.RemoveTorrent(bogus); err == nil {
		t.Error("RemoveTorrent unknown infohash should error")
	}
	if err := c.SetTorrentIndexing(bogus, true); err == nil {
		t.Error("SetTorrentIndexing unknown infohash should error")
	}
	if _, err := c.TorrentFiles(bogus); err == nil {
		t.Error("TorrentFiles unknown infohash should error")
	}
	if err := c.SetFilePriority(bogus, 0, "normal"); err == nil {
		t.Error("SetFilePriority unknown infohash should error")
	}
}
