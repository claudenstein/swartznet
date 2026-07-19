package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestCompanionFetchRefusesLiveTorrentCollision is the regression for the HIGH
// bug where a followed publisher's pointer infohash colliding with a real
// torrent the victim runs let a companion fetch adopt, download-all, and DROP
// that live torrent. A companion fetch/drop must NEVER touch a non-companion
// torrent.
func TestCompanionFetchRefusesLiveTorrentCollision(t *testing.T) {
	e := testEngine(t)
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i + 1)
	}

	// A real (non-companion) torrent the user is running.
	real, err := e.AddInfoHash(metainfo.Hash(ih))
	if err != nil {
		t.Fatal(err)
	}
	if real.companion {
		t.Fatal("a normal AddInfoHash was marked companion")
	}

	// A companion fetch for the SAME infohash must REFUSE — never adopt it.
	if _, err := e.FetchCompanionTorrent(context.Background(), ih); err == nil ||
		!strings.Contains(err.Error(), "collides with a live torrent") {
		t.Fatalf("FetchCompanionTorrent collision: err = %v, want a refusal", err)
	}
	if !e.hasHandle(ih) {
		t.Fatal("HIGH: the live torrent was torn down by a colliding companion fetch")
	}

	// A direct DropCompanionTorrent must also be a no-op on the real torrent.
	if err := e.DropCompanionTorrent(ih); err != nil {
		t.Fatal(err)
	}
	if !e.hasHandle(ih) {
		t.Fatal("HIGH: DropCompanionTorrent removed a non-companion torrent")
	}
}

// hasHandle reports whether the engine still holds a handle for ih.
func (e *Engine) hasHandle(ih [20]byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.handles[metainfo.Hash(ih)]
	return ok
}
