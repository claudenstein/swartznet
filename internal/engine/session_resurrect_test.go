package engine

import (
	"strings"
	"testing"
)

// TestSessionUpdateExistingDoesNotResurrect is the regression for the removed-
// torrent resurrection: persistState (pause/resume/set-indexing) now uses
// updateExisting, which must NOT re-insert an entry a concurrent RemoveTorrent
// already deleted — otherwise the removed torrent silently rejoins its swarm on
// the next restart.
func TestSessionUpdateExistingDoesNotResurrect(t *testing.T) {
	s, err := loadSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ih := strings.Repeat("a", 40)

	// Add, then remove (simulating RemoveTorrent).
	if err := s.update(ih, func(e *sessionEntry) { e.Paused = false }); err != nil {
		t.Fatal(err)
	}
	s.remove(ih)

	// A racing persistState-style mutation must be a NO-OP on the removed entry.
	existed, err := s.updateExisting(ih, func(e *sessionEntry) { e.Paused = true })
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Error("updateExisting reported a removed entry as existing")
	}
	s.mu.Lock()
	_, present := s.entries[ih]
	s.mu.Unlock()
	if present {
		t.Error("HIGH-ish: a removed session entry was resurrected by a state mutation")
	}

	// Contrast: the unconditional update WOULD resurrect it (documents the bug).
	if err := s.update(ih, func(e *sessionEntry) { e.Paused = true }); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, back := s.entries[ih]
	s.mu.Unlock()
	if !back {
		t.Error("sanity: update should upsert")
	}
}
