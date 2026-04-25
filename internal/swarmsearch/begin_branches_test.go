package swarmsearch

import "testing"

// TestBeginRejectsNonInitiator — Begin is the initiator-side
// entry point; calling it on a responder session is a misuse.
func TestBeginRejectsNonInitiator(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(1, RoleResponder, nil)
	if _, err := s.Begin(SyncFilter{}); err == nil {
		t.Error("Begin on responder session should fail")
	}
}

// TestBeginRejectsWrongPhase — calling Begin twice on the
// same initiator advances phase past PhaseIdle, so the
// second call hits the wrong-phase guard.
func TestBeginRejectsWrongPhase(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(1, RoleInitiator, nil)
	if _, err := s.Begin(SyncFilter{}); err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	if _, err := s.Begin(SyncFilter{}); err == nil {
		t.Error("second Begin should fail (wrong phase)")
	}
}
