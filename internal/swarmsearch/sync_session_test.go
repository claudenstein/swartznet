package swarmsearch

import (
	"fmt"
	"testing"
)

// makeLocalRecord synthesises a LocalRecord for a given keyword,
// deterministically so repeated calls with the same args produce
// identical records (and thus identical element IDs).
func makeLocalRecord(kw string, ihByte byte, ts int64) LocalRecord {
	var r LocalRecord
	r.Pk[0] = 0x11 // any fixed value
	r.Kw = kw
	r.Ih[0] = ihByte
	r.T = ts
	// Pow and Sig left zero; session wrapper doesn't verify them.
	return r
}

// End-to-end RIBLT sync between two in-process sessions. No
// I/O — the test pushes frames directly between the two sides.
func TestSyncSessionEndToEnd(t *testing.T) {
	// Sender has {a, b, c, d}; receiver has {a, b, e}.
	// After sync: added = {c, d}, removed = {e}.
	senderRecs := []LocalRecord{
		makeLocalRecord("a", 1, 1),
		makeLocalRecord("b", 2, 2),
		makeLocalRecord("c", 3, 3),
		makeLocalRecord("d", 4, 4),
	}
	receiverRecs := []LocalRecord{
		makeLocalRecord("a", 1, 1),
		makeLocalRecord("b", 2, 2),
		makeLocalRecord("e", 5, 5),
	}

	initiator := NewSyncSession(42, RoleInitiator, receiverRecs)
	responder := NewSyncSession(42, RoleResponder, senderRecs)

	// Initiator opens.
	begin, err := initiator.Begin(SyncFilter{})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := responder.ApplyBegin(begin); err != nil {
		t.Fatalf("ApplyBegin: %v", err)
	}

	// Stream up to 500 symbols; break when initiator decodes the diff
	// for a few consecutive stable rounds.
	stable := 0
	lastDecoded := 0
	for i := 0; i < 500; i++ {
		syms, _, err := responder.ProduceSymbols(20)
		if err != nil {
			t.Fatalf("ProduceSymbols: %v", err)
		}
		symsMsg := SyncSymbols{TxID: 42, Symbols: syms}
		if err := initiator.ApplySymbols(symsMsg); err != nil {
			t.Fatalf("ApplySymbols: %v", err)
		}
		decoded := len(initiator.dec.Added()) + len(initiator.dec.Removed())
		if decoded == lastDecoded {
			stable++
		} else {
			stable = 0
			lastDecoded = decoded
		}
		if stable >= 2 && initiator.Converged() {
			break
		}
	}

	needs := initiator.NeedIDs()
	if len(needs) != 2 {
		t.Fatalf("needs = %d, want 2 (c, d)", len(needs))
	}
	removed := initiator.RemovedIDs()
	if len(removed) != 1 {
		t.Fatalf("removed = %d, want 1 (e)", len(removed))
	}

	// Initiator requests the two records it needs.
	needMsg, err := initiator.NeedFrame(needs)
	if err != nil {
		t.Fatal(err)
	}
	recs, missing, err := responder.ApplyNeed(needMsg)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Errorf("responder delivered %d records, want 2", len(recs))
	}
	if len(missing) != 0 {
		t.Errorf("responder reported %d missing, want 0", len(missing))
	}

	recordsMsg, err := responder.BuildRecordsFrame(recs, missing)
	if err != nil {
		t.Fatal(err)
	}
	delivered, err := initiator.ApplyRecords(recordsMsg)
	if err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 2 {
		t.Errorf("initiator received %d records, want 2", len(delivered))
	}

	// Both sides close.
	initiator.Finish(SyncStatusConverged)
	_ = responder.Finish(SyncStatusConverged)
}

// A responder with a bigger set still converges — stresses the
// "one-sided diff" case from P3.1 but through the session
// wrapper.
func TestSyncSessionOneSidedDiff(t *testing.T) {
	sender := make([]LocalRecord, 50)
	for i := range sender {
		sender[i] = makeLocalRecord(fmt.Sprintf("kw-%d", i), byte(i), int64(i))
	}
	// Receiver has only the first 10.
	receiver := sender[:10]

	ini := NewSyncSession(1, RoleInitiator, receiver)
	resp := NewSyncSession(1, RoleResponder, sender)
	begin, _ := ini.Begin(SyncFilter{})
	if err := resp.ApplyBegin(begin); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 300; i++ {
		syms, _, err := resp.ProduceSymbols(20)
		if err != nil {
			t.Fatal(err)
		}
		_ = ini.ApplySymbols(SyncSymbols{TxID: 1, Symbols: syms})
		if ini.Converged() && len(ini.NeedIDs()) == 40 {
			break
		}
	}
	if len(ini.NeedIDs()) != 40 {
		t.Errorf("needs = %d, want 40", len(ini.NeedIDs()))
	}
}

// Phase machine rejects out-of-order calls.
func TestSyncSessionPhaseGuards(t *testing.T) {
	s := NewSyncSession(1, RoleInitiator, nil)

	// Applying records before Begin should fail.
	if _, err := s.ApplyRecords(SyncRecords{TxID: 1}); err != nil && s.Phase() != PhaseIdle {
		t.Errorf("Phase drift after ApplyRecords error: %d", s.Phase())
	}

	// Responder tries to ApplyBegin as an initiator — error.
	if err := s.ApplyBegin(SyncBegin{TxID: 1}); err == nil {
		t.Error("ApplyBegin on initiator session should fail")
	}
}

// TxID mismatch is a protocol error.
func TestSyncSessionTxIDGuard(t *testing.T) {
	ini := NewSyncSession(100, RoleInitiator, nil)
	if _, err := ini.Begin(SyncFilter{}); err != nil {
		t.Fatal(err)
	}
	// Symbols frame with wrong TxID.
	if err := ini.ApplySymbols(SyncSymbols{TxID: 999, Symbols: []SyncSymbol{
		{Count: 1, KeyXOR: 0, DataXOR: make([]byte, 32)},
	}}); err == nil {
		t.Error("expected TxID-mismatch error")
	}
}

// Budget: session aborts when producing more symbols than maxSymbols.
func TestSyncSessionBudgetEnforced(t *testing.T) {
	recs := []LocalRecord{makeLocalRecord("x", 1, 1)}
	s := NewSyncSession(1, RoleResponder, recs)
	if err := s.ApplyBegin(SyncBegin{TxID: 1, ElementSize: 32}); err != nil {
		t.Fatal(err)
	}
	s.SetBudgets(5, 0) // allow only 5 symbols total

	// First batch of up to 20 is clamped by MaxSymbolsPerMessage but
	// also by maxSymbols=5.
	syms, _, err := s.ProduceSymbols(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 5 {
		t.Errorf("first batch = %d, want 5 (budget cap)", len(syms))
	}

	// A second call should return ErrSymbolBudgetExceeded.
	if _, _, err := s.ProduceSymbols(1); err != ErrSymbolBudgetExceeded {
		t.Errorf("want ErrSymbolBudgetExceeded, got %v", err)
	}
}

// Producing the SyncNeed frame with empty IDs signals "I'm done".
func TestSyncNeedFrameEmpty(t *testing.T) {
	s := NewSyncSession(1, RoleInitiator, nil)
	if _, err := s.Begin(SyncFilter{}); err != nil {
		t.Fatal(err)
	}
	need, err := s.NeedFrame(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(need.IDs) != 0 {
		t.Errorf("empty need should have zero ids, got %d", len(need.IDs))
	}
	if s.Phase() != PhaseNeeded {
		t.Errorf("phase after empty Need = %d, want Needed", s.Phase())
	}
}

// Finish and ApplyEnd both move phase to Ended.
func TestSyncSessionFinishApplyEnd(t *testing.T) {
	s1 := NewSyncSession(7, RoleInitiator, nil)
	_, _ = s1.Begin(SyncFilter{})
	end := s1.Finish(SyncStatusConverged)
	if s1.Phase() != PhaseEnded {
		t.Errorf("Finish phase = %d, want Ended", s1.Phase())
	}

	s2 := NewSyncSession(7, RoleResponder, nil)
	if err := s2.ApplyEnd(end); err != nil {
		t.Fatal(err)
	}
	if s2.Phase() != PhaseEnded {
		t.Errorf("ApplyEnd phase = %d, want Ended", s2.Phase())
	}
}

// RecordByID looks up a local record by its RIBLT element ID.
func TestSyncSessionRecordByID(t *testing.T) {
	r := makeLocalRecord("find-me", 9, 99)
	id := localRecordID(r)
	s := NewSyncSession(1, RoleResponder, []LocalRecord{r})
	got, ok := s.RecordByID(id)
	if !ok {
		t.Fatal("RecordByID missed a record we just stored")
	}
	if got.Kw != "find-me" {
		t.Errorf("wrong record returned: %+v", got)
	}

	var bogus [32]byte
	if _, ok := s.RecordByID(bogus); ok {
		t.Error("expected miss for unknown id")
	}
}

// localRecordID must be deterministic for identical input.
func TestLocalRecordIDDeterministic(t *testing.T) {
	r := makeLocalRecord("det", 7, 77)
	a := localRecordID(r)
	b := localRecordID(r)
	if a != b {
		t.Fatalf("non-deterministic id: %x vs %x", a, b)
	}
}
