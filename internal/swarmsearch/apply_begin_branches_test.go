package swarmsearch

import "testing"

// TestApplyBeginRejectsWrongPhase — calling ApplyBegin twice
// on the same responder session is a protocol error: the first
// call moves phase to PhaseBegun, the second sees the wrong
// phase and rejects.
func TestApplyBeginRejectsWrongPhase(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(1, RoleResponder, nil)
	if err := s.ApplyBegin(SyncBegin{TxID: 1, ElementSize: 32}); err != nil {
		t.Fatalf("first ApplyBegin: %v", err)
	}
	if err := s.ApplyBegin(SyncBegin{TxID: 1, ElementSize: 32}); err == nil {
		t.Error("second ApplyBegin should fail (wrong phase)")
	}
}

// TestApplyBeginRejectsTxIDMismatch — sync_begin frames whose
// txid doesn't match the session's expected txid are bogus.
func TestApplyBeginRejectsTxIDMismatch(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(42, RoleResponder, nil)
	if err := s.ApplyBegin(SyncBegin{TxID: 99, ElementSize: 32}); err == nil {
		t.Error("ApplyBegin with wrong txid should fail")
	}
}

// TestApplyBeginRejectsBadElementSize — only ElementSize=32 is
// supported (matches the RIBLT cell DataXOR size).
func TestApplyBeginRejectsBadElementSize(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(1, RoleResponder, nil)
	if err := s.ApplyBegin(SyncBegin{TxID: 1, ElementSize: 16}); err == nil {
		t.Error("ApplyBegin with ElementSize=16 should fail")
	}
}

// TestApplyBeginClampsBudgets — when m.MaxSymbols / m.MaxBytes
// are smaller than the session's defaults, ApplyBegin honours
// the more restrictive caller-supplied limit. ProduceSymbols is
// the cleanest observation point for the clamp.
func TestApplyBeginClampsBudgets(t *testing.T) {
	t.Parallel()
	recs := []LocalRecord{
		makeLocalRecord("a", 0x01, 1),
		makeLocalRecord("a", 0x02, 2),
		makeLocalRecord("a", 0x03, 3),
	}
	s := NewSyncSession(1, RoleResponder, recs)
	// Caller asks for at most 4 symbols. Default maxSymbols is
	// well above 4 so this clamp must be visible.
	if err := s.ApplyBegin(SyncBegin{TxID: 1, ElementSize: 32, MaxSymbols: 4, MaxBytes: 1024}); err != nil {
		t.Fatalf("ApplyBegin: %v", err)
	}
	syms, _, err := s.ProduceSymbols(MaxSymbolsPerMessage)
	if err != nil {
		t.Fatalf("ProduceSymbols: %v", err)
	}
	if len(syms) > 4 {
		t.Errorf("ProduceSymbols returned %d symbols, expected ≤4 (caller-supplied MaxSymbols)", len(syms))
	}
}
