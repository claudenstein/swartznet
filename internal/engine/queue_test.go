package engine_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/engine"
)

func TestMaxActiveDownloadsDefaultsUnlimited(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	if got := eng.MaxActiveDownloads(); got != 0 {
		t.Errorf("default max active: got %d, want 0 (unlimited)", got)
	}
}

func TestMaxActiveDownloadsClampsNegative(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	eng.SetMaxActiveDownloads(-5)
	if got := eng.MaxActiveDownloads(); got != 0 {
		t.Errorf("after set -5: got %d, want 0", got)
	}
}

// TestQueueOrderThirdTorrentQueuedUnderCap2 adds 3 torrents with
// the cap set to 2 and verifies the third ends up "queued".
func TestQueueOrderThirdTorrentQueuedUnderCap2(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)
	eng.SetMaxActiveDownloads(2)

	dir := t.TempDir()
	var infoHashes []string
	for _, name := range []string{"a", "b", "c"} {
		root := filepath.Join(dir, name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, "file.bin"), []byte(fillTo(32*1024)), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: root})
		if err != nil {
			t.Fatalf("CreateTorrent %s: %v", name, err)
		}
		if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
			t.Fatalf("AddTorrentMetaInfo %s: %v", name, err)
		}
		infoHashes = append(infoHashes, mi.HashInfoBytes().HexString())
	}

	// Wait up to 5 s for autoDownload goroutines to run.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snaps := eng.TorrentSnapshots()
		queuedCount := 0
		activeCount := 0
		for _, s := range snaps {
			if s.Queued {
				queuedCount++
			} else if s.Status == "downloading" || s.Status == "seeding" {
				activeCount++
			}
		}
		if queuedCount+activeCount == len(infoHashes) && queuedCount >= 1 {
			// At most 2 active, rest queued, under cap 2.
			if activeCount > 2 {
				t.Fatalf("active count %d exceeds cap 2", activeCount)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	snaps := eng.TorrentSnapshots()
	t.Fatalf("timeout waiting for queue to settle; snapshots: %+v", snaps)
}

// TestQueueRaisingCapPromotesQueued sets cap=1, adds 3 torrents
// (most should be queued since content completes instantly when
// seeded from local disk — but the Queued flag is still the thing
// we care about), then raises the cap to 0 and verifies nothing
// is queued any more.
func TestQueueRaisingCapPromotesQueued(t *testing.T) {
	t.Parallel()

	eng := newTestEngine(t)
	eng.SetMaxActiveDownloads(1)

	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		root := filepath.Join(dir, name)
		_ = os.MkdirAll(root, 0o755)
		_ = os.WriteFile(filepath.Join(root, "file.bin"), []byte(fillTo(32*1024)), 0o644)
		mi, _ := eng.CreateTorrent(engine.CreateTorrentOptions{Root: root})
		_, _ = eng.AddTorrentMetaInfo(mi)
	}

	// Wait for at least one to be queued.
	waitForSnapshot(t, eng, func(snaps []engine.TorrentSnapshot) bool {
		for _, s := range snaps {
			if s.Queued {
				return true
			}
		}
		return false
	}, 5*time.Second)

	// Raise cap to 0 (unlimited) — should promote all queued.
	eng.SetMaxActiveDownloads(0)

	waitForSnapshot(t, eng, func(snaps []engine.TorrentSnapshot) bool {
		for _, s := range snaps {
			if s.Queued {
				return false
			}
		}
		return true
	}, 5*time.Second)
}

// waitForSnapshot polls until pred returns true or timeout.
func waitForSnapshot(t *testing.T, eng *engine.Engine, pred func([]engine.TorrentSnapshot) bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snaps := eng.TorrentSnapshots()
		if pred(snaps) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for predicate; last snapshots: %+v", eng.TorrentSnapshots())
}
