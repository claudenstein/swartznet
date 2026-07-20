package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionRemoveReturnsSaveError pins the round-7 LOW fix: session.remove must
// SURFACE (not swallow) a persistence failure, so a non-durable removal — which
// would resurrect the torrent on the next restart — is at least observable. Here
// the session path is inside a non-existent directory, so saveLocked's write
// fails.
func TestSessionRemoveReturnsSaveError(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "does-not-exist", "session.json")
	s := &session{
		path:    badPath,
		entries: map[string]sessionEntry{},
	}
	ih := strings.Repeat("c", 40)
	s.entries[ih] = sessionEntry{InfoHash: ih}

	err := s.remove(ih)
	if err == nil {
		t.Error("remove swallowed a save error on an unwritable session path")
	}
	// The in-memory removal still applies (the torrent is stopped this run).
	if _, ok := s.entries[ih]; ok {
		t.Error("entry not removed in-memory despite the save failure")
	}
}

// TestSessionRemoveSucceedsPersists confirms the happy path still returns nil.
func TestSessionRemoveSucceedsPersists(t *testing.T) {
	s, err := loadSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ih := strings.Repeat("d", 40)
	if err := s.update(ih, func(e *sessionEntry) {}); err != nil {
		t.Fatal(err)
	}
	if err := s.remove(ih); err != nil {
		t.Errorf("remove on a writable session returned %v, want nil", err)
	}
}
