package engine

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anacrolix/torrent"
)

// TestQueueOrActivateConcurrentRespectsCap covers the promoteMu
// serialization added to queueOrActivate. With maxActiveDownloads=1,
// firing many queueOrActivate calls concurrently (the shape of a
// batch RestoreSession where each handle's autoDownload goroutine
// races on countActiveDownloads) must never activate more than one
// torrent at a time. Without the lock, several callers observe
// active < cap simultaneously and over-subscribe.
func TestQueueOrActivateConcurrentRespectsCap(t *testing.T) {
	t.Parallel()
	eng := newPausedSkipEngine(t)
	eng.SetMaxActiveDownloads(1)

	// Create source files OUTSIDE DataDir so the torrents never
	// verify as complete (BytesMissing stays > 0) and therefore keep
	// occupying a download slot for the duration of the test. This
	// makes the cap accounting deterministic.
	srcDir := t.TempDir()
	const n = 8
	handles := make([]*Handle, 0, n)
	for i := 0; i < n; i++ {
		// distinct content per torrent so infohashes differ
		buf := make([]byte, 32*1024)
		for j := range buf {
			buf[j] = byte(i + 1)
		}
		p := filepath.Join(srcDir, "f"+string(rune('a'+i))+".bin")
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		mi, err := eng.CreateTorrent(CreateTorrentOptions{Root: p})
		if err != nil {
			t.Fatalf("CreateTorrent: %v", err)
		}
		hAny, err := eng.AddTorrentMetaInfo(mi)
		if err != nil {
			t.Fatalf("AddTorrentMetaInfo: %v", err)
		}
		h := hAny.(*Handle)
		// Reset to None + unqueued so the activation decision is fresh
		// and we can observe exactly what queueOrActivate flips.
		h.setQueued(false)
		for _, f := range h.T.Files() {
			f.SetPriority(torrent.PiecePriorityNone)
		}
		handles = append(handles, h)
	}

	var wg sync.WaitGroup
	for _, h := range handles {
		wg.Add(1)
		go func(h *Handle) {
			defer wg.Done()
			eng.queueOrActivate(h)
		}(h)
	}
	wg.Wait()

	// Count how many were activated (any file flipped to Normal).
	activated := 0
	for _, h := range handles {
		for _, f := range h.T.Files() {
			if f.Priority() == torrent.PiecePriorityNormal {
				activated++
				break
			}
		}
	}
	if activated > 1 {
		t.Fatalf("activated %d torrents under maxActiveDownloads=1; cap was over-subscribed", activated)
	}
}
