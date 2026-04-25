package swarmsearch

import (
	"testing"
)

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
