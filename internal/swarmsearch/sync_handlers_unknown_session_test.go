package swarmsearch

import (
	"log/slog"
	"testing"
)

// TestOnSyncSymbolsUnknownSession — receiving SyncSymbols for a
// peer with no registered (peer, txid) entry must short-circuit
// without panicking. Mirrors the corresponding test on
// onSyncRecords.
func TestOnSyncSymbolsUnknownSession(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	p.onSyncSymbols("never-registered:1", SyncSymbols{TxID: 99})
}

// TestOnSyncSymbolsApplyError — registered session in the wrong
// phase (PhaseIdle on a responder) makes ApplySymbols error;
// the handler logs and returns silently.
func TestOnSyncSymbolsApplyError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	sess := NewSyncSession(7, RoleResponder, nil)
	p.registerSyncSession("p:1", sess)
	// PhaseIdle ApplySymbols returns an error — exercise that arm.
	p.onSyncSymbols("p:1", SyncSymbols{TxID: 7})
}

// TestOnSyncNeedUnknownSession — same shape as the two
// previous; the build/apply error arms only fire when sess !=
// nil, so this just walks the early-return path.
func TestOnSyncNeedUnknownSession(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	p.onSyncNeed("never-registered:2", SyncNeed{TxID: 12345}, func([]byte) error { return nil })
}

// TestOnSyncNeedApplyError — ApplyNeed rejects sync_need
// frames with bad-length IDs (must be 32 bytes). Feed it a
// 16-byte ID to trip the error path; handler must log
// apply_err and return without sending a records frame.
func TestOnSyncNeedApplyError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	sess := NewSyncSession(8, RoleResponder, nil)
	p.registerSyncSession("p:2", sess)
	var replyCalled bool
	reply := func([]byte) error {
		replyCalled = true
		return nil
	}
	// 16-byte ID violates the 32-byte invariant ApplyNeed enforces.
	bad := []byte{0x01, 0x02}
	for len(bad) < 16 {
		bad = append(bad, 0)
	}
	p.onSyncNeed("p:2", SyncNeed{TxID: 8, IDs: [][]byte{bad}}, reply)
	if replyCalled {
		t.Error("apply_err path should not call the reply closure")
	}
}
