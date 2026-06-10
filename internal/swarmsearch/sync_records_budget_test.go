package swarmsearch

import (
	"errors"
	"io"
	"log/slog"
	"testing"
)

// goodSizedRecord returns a record that passes ApplyRecords'
// wire-level size checks. The signature is garbage — that's fine
// for session-level tests, which treat records as opaque.
func goodSizedRecord(kw string) SyncRecord {
	return SyncRecord{
		Pk:  make([]byte, 32),
		Kw:  kw,
		Ih:  make([]byte, 20),
		Sig: make([]byte, 64),
	}
}

// needySession builds an initiator session driven to PhaseNeeded
// (Begin + NeedFrame), the phase in which inbound sync_records
// frames are legitimate.
func needySession(t *testing.T, txid uint32) *SyncSession {
	t.Helper()
	s := NewSyncSession(txid, RoleInitiator, nil)
	if _, err := s.Begin(SyncFilter{}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := s.NeedFrame(nil); err != nil {
		t.Fatalf("NeedFrame: %v", err)
	}
	return s
}

// TestApplyRecordsRejectsWrongPhase — a peer firing sync_records
// before this side asked for them via sync_need is a protocol
// violation. This is the exact shape of the unbounded-ingest
// attack: sync_begin followed immediately by sync_records floods.
func TestApplyRecordsRejectsWrongPhase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T) *SyncSession
	}{
		{"idle", func(t *testing.T) *SyncSession {
			return NewSyncSession(1, RoleInitiator, nil)
		}},
		{"begun", func(t *testing.T) *SyncSession {
			s := NewSyncSession(1, RoleInitiator, nil)
			if _, err := s.Begin(SyncFilter{}); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			return s
		}},
		{"symbols_flowing", func(t *testing.T) *SyncSession {
			s := NewSyncSession(1, RoleInitiator, nil)
			if _, err := s.Begin(SyncFilter{}); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			if err := s.ApplySymbols(SyncSymbols{TxID: 1}); err != nil {
				t.Fatalf("ApplySymbols: %v", err)
			}
			return s
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.setup(t)
			if _, err := s.ApplyRecords(SyncRecords{TxID: 1, Records: []SyncRecord{goodSizedRecord("kw")}}); err == nil {
				t.Errorf("ApplyRecords in phase %q should fail", tc.name)
			}
		})
	}
}

// TestApplyRecordsEnforcesByteBudget — bytesIn must accumulate
// per frame and crossing maxBytes must return the sentinel error
// (the documented SPEC §2.3 limit_exceeded path). Before the fix
// the budget fields were declared and echoed on the wire but
// never incremented or compared.
func TestApplyRecordsEnforcesByteBudget(t *testing.T) {
	t.Parallel()
	s := needySession(t, 1)
	// One record costs 132 + len(kw) bytes; "k" → 133. Budget of
	// 200 admits one frame and rejects the second.
	s.SetBudgets(0, 200)

	if _, err := s.ApplyRecords(SyncRecords{TxID: 1, Records: []SyncRecord{goodSizedRecord("k")}}); err != nil {
		t.Fatalf("first frame under budget should pass: %v", err)
	}
	if s.bytesIn != 133 {
		t.Errorf("bytesIn = %d, want 133", s.bytesIn)
	}
	if s.recordsIn != 1 {
		t.Errorf("recordsIn = %d, want 1", s.recordsIn)
	}

	_, err := s.ApplyRecords(SyncRecords{TxID: 1, Records: []SyncRecord{goodSizedRecord("k")}})
	if !errors.Is(err, ErrSyncBytesBudgetExceeded) {
		t.Fatalf("second frame should exceed budget, got %v", err)
	}
	// The violating frame must not be ingested into the counters.
	if s.recordsIn != 1 {
		t.Errorf("recordsIn after violation = %d, want 1", s.recordsIn)
	}
	// Finish must echo the cumulative counters for sync_end.
	end := s.Finish(SyncStatusLimitExceeded)
	if end.BytesIn != 133 || end.Decoded != 1 {
		t.Errorf("Finish echoed bytes_in=%d decoded=%d, want 133/1", end.BytesIn, end.Decoded)
	}
}

// TestApplyRecordsCountsMissingIDsAgainstBudget — missing IDs are
// peer-controlled payload too and must count toward max_bytes.
func TestApplyRecordsCountsMissingIDsAgainstBudget(t *testing.T) {
	t.Parallel()
	s := needySession(t, 1)
	s.SetBudgets(0, 100)
	// 4 × 32-byte missing IDs = 128 bytes > 100.
	missing := [][]byte{
		make([]byte, 32), make([]byte, 32),
		make([]byte, 32), make([]byte, 32),
	}
	_, err := s.ApplyRecords(SyncRecords{TxID: 1, Missing: missing})
	if !errors.Is(err, ErrSyncBytesBudgetExceeded) {
		t.Fatalf("missing-only frame should exceed budget, got %v", err)
	}
}

// TestOnSyncRecordsBudgetViolationFailsClosed — handler-level
// regression for the unbounded sync_records ingest: once a frame
// crosses the session's max_bytes budget the handler must send
// the documented sync_end limit_exceeded, release the session,
// and charge misbehavior — not log-and-limp.
func TestOnSyncRecordsBudgetViolationFailsClosed(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "9.9.9.9:6881"

	sess := needySession(t, 5)
	sess.SetBudgets(0, 150)
	p.registerSyncSession(peer, sess)

	var sent [][]byte
	reply := func(b []byte) error {
		sent = append(sent, b)
		return nil
	}

	// First frame (133 bytes) is under budget — no teardown.
	p.onSyncRecords(peer, SyncRecords{TxID: 5, Records: []SyncRecord{goodSizedRecord("k")}}, reply)
	if p.lookupSyncSession(peer, 5) == nil {
		t.Fatal("under-budget frame must not tear down the session")
	}
	if len(sent) != 0 {
		t.Fatalf("under-budget frame sent %d frames, want 0", len(sent))
	}

	// Second frame crosses the budget → fail closed.
	p.onSyncRecords(peer, SyncRecords{TxID: 5, Records: []SyncRecord{goodSizedRecord("k")}}, reply)
	if p.lookupSyncSession(peer, 5) != nil {
		t.Error("session should be released after budget violation")
	}
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("budget violation should charge misbehavior")
	}
	if len(sent) != 1 {
		t.Fatalf("violation sent %d frames, want 1 sync_end", len(sent))
	}
	end, err := DecodeSyncEnd(sent[0])
	if err != nil {
		t.Fatalf("DecodeSyncEnd: %v", err)
	}
	if end.Status != SyncStatusLimitExceeded {
		t.Errorf("sync_end status = %q, want %q", end.Status, SyncStatusLimitExceeded)
	}
}

// TestOnSyncRecordsPhaseViolationFailsClosed — the original
// attack: sync_records straight after sync_begin (responder
// session in PhaseSymbolsFlowing) must be aborted, released, and
// charged instead of forcing signature verification work.
func TestOnSyncRecordsPhaseViolationFailsClosed(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "8.8.8.8:6881"

	sess := NewSyncSession(9, RoleResponder, nil)
	if err := sess.ApplyBegin(SyncBegin{TxID: 9, ElementSize: 32}); err != nil {
		t.Fatalf("ApplyBegin: %v", err)
	}
	p.registerSyncSession(peer, sess)

	var sent [][]byte
	reply := func(b []byte) error {
		sent = append(sent, b)
		return nil
	}
	p.onSyncRecords(peer, SyncRecords{TxID: 9, Records: []SyncRecord{goodSizedRecord("kw")}}, reply)

	if p.lookupSyncSession(peer, 9) != nil {
		t.Error("session should be released after phase violation")
	}
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("phase violation should charge misbehavior")
	}
	if len(sent) != 1 {
		t.Fatalf("violation sent %d frames, want 1 sync_end", len(sent))
	}
	end, err := DecodeSyncEnd(sent[0])
	if err != nil {
		t.Fatalf("DecodeSyncEnd: %v", err)
	}
	if end.Status != SyncStatusAborted {
		t.Errorf("sync_end status = %q, want %q", end.Status, SyncStatusAborted)
	}
}

// TestOnSyncSymbolsBudgetViolationFailsClosed — ApplySymbols
// errors (here: ErrSymbolBudgetExceeded) must tear the session
// down with sync_end limit_exceeded + release + charge, instead
// of leaving it in limbo for the reaper while the peer keeps
// firing post-budget frames.
func TestOnSyncSymbolsBudgetViolationFailsClosed(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	const peer = "7.7.7.7:6881"

	sess := NewSyncSession(3, RoleInitiator, nil)
	if _, err := sess.Begin(SyncFilter{}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	sess.SetBudgets(1, 0) // one symbol max
	p.registerSyncSession(peer, sess)

	var sent [][]byte
	reply := func(b []byte) error {
		sent = append(sent, b)
		return nil
	}
	two := []SyncSymbol{
		{Count: 1, DataXOR: make([]byte, 32)},
		{Count: 1, DataXOR: make([]byte, 32)},
	}
	p.onSyncSymbols(peer, SyncSymbols{TxID: 3, Symbols: two}, reply)

	if p.lookupSyncSession(peer, 3) != nil {
		t.Error("session should be released after symbol-budget violation")
	}
	if got := p.MisbehaviorScore(peer); got == 0 {
		t.Error("symbol-budget violation should charge misbehavior")
	}
	if len(sent) != 1 {
		t.Fatalf("violation sent %d frames, want 1 sync_end", len(sent))
	}
	end, err := DecodeSyncEnd(sent[0])
	if err != nil {
		t.Fatalf("DecodeSyncEnd: %v", err)
	}
	if end.Status != SyncStatusLimitExceeded {
		t.Errorf("sync_end status = %q, want %q", end.Status, SyncStatusLimitExceeded)
	}
}
