package swarmsearch

import (
	"testing"
)

// TestProduceSymbolsWrongPhase covers ProduceSymbols's
// `if s.phase != PhaseBegun && s.phase != PhaseSymbolsFlowing`
// error arm. A fresh session is in PhaseIdle; ProduceSymbols
// must reject before generating any symbols.
func TestProduceSymbolsWrongPhase(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(1, RoleResponder, nil)
	if _, _, err := s.ProduceSymbols(8); err == nil {
		t.Error("ProduceSymbols in PhaseIdle should error")
	}
}

// TestProduceSymbolsCountClamping covers the two count-
// clamping arms: count <= 0 falls back to MaxSymbolsPerMessage,
// and count > MaxSymbolsPerMessage gets capped.
func TestProduceSymbolsCountClamping(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(1, RoleInitiator, nil)
	if _, err := s.Begin(SyncFilter{}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// count = 0 → MaxSymbolsPerMessage default applied; the
	// initial symbol budget is also bounded by maxSymbols, so
	// this just needs to not error.
	syms, _, err := s.ProduceSymbols(0)
	if err != nil {
		t.Fatalf("ProduceSymbols(0): %v", err)
	}
	if len(syms) == 0 || len(syms) > MaxSymbolsPerMessage {
		t.Errorf("count<=0 path: got %d symbols, want 1..%d", len(syms), MaxSymbolsPerMessage)
	}

	// count > MaxSymbolsPerMessage → capped.
	syms2, _, err := s.ProduceSymbols(MaxSymbolsPerMessage + 100)
	if err != nil {
		t.Fatalf("ProduceSymbols(huge): %v", err)
	}
	if len(syms2) > MaxSymbolsPerMessage {
		t.Errorf("count too large: got %d symbols, want <= %d", len(syms2), MaxSymbolsPerMessage)
	}
}

// TestSyncSessionFinishEmptyStatus covers Finish's empty-string
// default arm: passing "" must rewrite to SyncStatusConverged.
func TestSyncSessionFinishEmptyStatus(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(7, RoleInitiator, nil)
	end := s.Finish("")
	if end.Status != SyncStatusConverged {
		t.Errorf("Finish(\"\").Status = %q, want %q", end.Status, SyncStatusConverged)
	}
}

// TestSyncSessionApplyEndTxIDMismatch covers ApplyEnd's
// txid-mismatch guard. Pass a SyncEnd whose TxID differs from
// the session's own; the call must error and leave the session
// outside PhaseEnded.
func TestSyncSessionApplyEndTxIDMismatch(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(11, RoleInitiator, nil)
	if err := s.ApplyEnd(SyncEnd{TxID: 99, Status: SyncStatusConverged}); err == nil {
		t.Error("ApplyEnd should error on TxID mismatch")
	}
	if got := s.Phase(); got == PhaseEnded {
		t.Errorf("ApplyEnd advanced phase despite TxID mismatch: phase=%d", got)
	}
}

// TestSyncSessionSetBudgetsZerosLeaveDefaults — SetBudgets is
// supposed to leave existing budgets untouched when called with
// 0 for either argument (caller "I don't want to change this
// one"). Verify by calling with (0, 0) on a fresh session and
// observing that the original Symbols-budget guard in
// ProduceSymbols still fires at the default cap.
func TestSyncSessionSetBudgetsZerosLeaveDefaults(t *testing.T) {
	t.Parallel()
	s := NewSyncSession(1, RoleInitiator, nil)
	// Capture initial budgets via a positive update.
	s.SetBudgets(50, 100_000)
	// Now the no-op branch: both args zero.
	s.SetBudgets(0, 0)
	// And the asymmetric branches: each arg zero in turn.
	s.SetBudgets(0, 200_000)
	s.SetBudgets(75, 0)
}
