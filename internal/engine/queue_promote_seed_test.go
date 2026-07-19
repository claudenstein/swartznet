package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPromoteQueuedDoesNotCountCompleteSeed pins the round-5 MED fix: a queued
// COMPLETE seed must not consume a download slot in promoteQueued. A complete
// seed can legitimately be queued (queued while the cap was full, then completed
// by the background verify); activating it clears the queued flag but it never
// occupies a slot (isActive()==false). The old unconditional active++ counted it
// anyway and returned before a genuinely-queued download ordered after it,
// starving that download until an unrelated event.
func TestPromoteQueuedDoesNotCountCompleteSeed(t *testing.T) {
	e, dataDir := seedEngine(t, time.Hour)
	e.SetMaxActiveDownloads(1)

	// A genuinely complete seed (isActive()==false once the verify finishes).
	seedRoot := filepath.Join(dataDir, "seed")
	mi := buildTextTorrent(t, seedRoot, "s.txt", []byte("hello world seed body for a complete torrent"))
	seedH, err := e.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(seedRoot, "s.txt"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for seedH.T.BytesMissing() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("seed never completed verify")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A metadata-only download (Info==nil => isActive()==true) that genuinely
	// occupies a slot.
	dlH, err := e.AddMagnet("magnet:?xt=urn:btih:" + strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}

	// Reproduce the buggy precondition: BOTH queued, the complete seed ordered
	// FIRST, nothing currently active.
	seedH.setQueueOrder(0)
	seedH.setQueued(true)
	dlH.setQueueOrder(1)
	dlH.setQueued(true)
	if n := e.countActive(); n != 0 {
		t.Fatalf("precondition: countActive=%d, want 0", n)
	}

	e.promoteQueued()

	if seedH.isActive() {
		t.Fatal("sanity: a complete seed must never be isActive()")
	}
	if dlH.isQueued() {
		t.Error("download starved: a queued COMPLETE seed consumed the single slot in promoteQueued")
	}
}
