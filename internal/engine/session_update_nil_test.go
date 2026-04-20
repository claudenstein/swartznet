package engine

import "testing"

// TestSessionUpdateNilReceiver covers the `s == nil → return nil`
// defensive branch of session.update.
func TestSessionUpdateNilReceiver(t *testing.T) {
	t.Parallel()
	var s *session
	if err := s.update("abc", func(*sessionEntry) {}); err != nil {
		t.Errorf("nil session update: %v, want nil", err)
	}
}

// TestSessionRemoveNilReceiver covers the same defensive branch
// in session.remove.
func TestSessionRemoveNilReceiver(t *testing.T) {
	t.Parallel()
	var s *session
	if err := s.remove("abc"); err != nil {
		t.Errorf("nil session remove: %v, want nil", err)
	}
}

// TestSessionListNilReceiver covers the defensive branch in
// session.list.
func TestSessionListNilReceiver(t *testing.T) {
	t.Parallel()
	var s *session
	if got := s.list(); got != nil {
		t.Errorf("nil session list = %v, want nil", got)
	}
}
