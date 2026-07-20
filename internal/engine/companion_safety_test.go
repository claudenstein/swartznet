package engine

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestCompanionDropGuardProtectsLiveTorrent is the regression for the HIGH bug
// where a followed publisher's pointer infohash colliding with a real torrent the
// victim runs could tear that torrent down via the companion drop path. A
// companion drop must NEVER remove a non-companion torrent, regardless of who
// names the infohash.
func TestCompanionDropGuardProtectsLiveTorrent(t *testing.T) {
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

	// DropCompanionTorrent — the path a companion fetch's deferred cleanup and a
	// followed-publisher's pointer would reach — must be a NO-OP on the real
	// torrent, not destroy it.
	if err := e.DropCompanionTorrent(ih); err != nil {
		t.Fatal(err)
	}
	if !e.hasHandle(ih) {
		t.Fatal("HIGH: DropCompanionTorrent removed a non-companion torrent")
	}

	// And the handle stays non-companion (its priorities/lifecycle are untouched).
	if got := e.handleCompanion(ih); got {
		t.Error("the real torrent's handle was flipped to companion")
	}
}

// hasHandle reports whether the engine still holds a handle for ih.
func (e *Engine) hasHandle(ih [20]byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.handles[metainfo.Hash(ih)]
	return ok
}

// handleCompanion reports the companion flag of the handle for ih (false if absent).
func (e *Engine) handleCompanion(ih [20]byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if h, ok := e.handles[metainfo.Hash(ih)]; ok {
		return h.companion
	}
	return false
}
