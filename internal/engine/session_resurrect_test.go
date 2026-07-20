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

// TestSessionUpdateGuardedSuppressesResurrect is the regression for #4: persistAdd
// runs AFTER Add releases e.mu, so a concurrent RemoveTorrent on the same infohash
// can delete the row before this create. updateGuarded must NOT create the entry
// when its abort predicate (the handle's removed flag) reports the add was
// cancelled — otherwise the removed torrent silently rejoins its swarm on restart.
func TestSessionUpdateGuardedSuppressesResurrect(t *testing.T) {
	s, err := loadSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ih := strings.Repeat("b", 40)

	// abort=true (handle already removed) → the create is suppressed.
	written, err := s.updateGuarded(ih, func() bool { return true }, func(e *sessionEntry) {
		e.AddedVia = "magnet"
	})
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Error("updateGuarded reported a write despite abort")
	}
	s.mu.Lock()
	_, present := s.entries[ih]
	s.mu.Unlock()
	if present {
		t.Error("HIGH: persistAdd resurrected a removed torrent's session entry")
	}

	// abort=false (live add) → the create proceeds (no over-suppression).
	written, err = s.updateGuarded(ih, func() bool { return false }, func(e *sessionEntry) {
		e.AddedVia = "magnet"
	})
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Error("updateGuarded suppressed a live add")
	}
	s.mu.Lock()
	_, present = s.entries[ih]
	s.mu.Unlock()
	if !present {
		t.Error("live add was not persisted")
	}
}
