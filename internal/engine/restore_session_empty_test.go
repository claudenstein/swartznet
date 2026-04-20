package engine_test

import (
	"testing"
)

// TestRestoreSessionEmptyList covers the `len(entries) == 0 →
// return nil` fast-return branch of RestoreSession. A freshly-
// constructed engine has no session entries yet; RestoreSession
// must succeed without error and without attempting any
// per-entry work. The existing restore tests all pre-seed at
// least one entry via writeSessionManifest, so this branch
// was untested.
func TestRestoreSessionEmptyList(t *testing.T) {
	t.Parallel()
	eng, cleanup := newAddTorrentFileEngine(t)
	defer cleanup()

	// No writeSessionManifest call — session is empty.
	if err := eng.RestoreSession(); err != nil {
		t.Errorf("RestoreSession on empty session: %v, want nil", err)
	}
	if got := len(eng.Torrents()); got != 0 {
		t.Errorf("Torrents() = %d, want 0 (nothing to restore)", got)
	}
}
