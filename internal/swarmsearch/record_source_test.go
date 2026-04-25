package swarmsearch

import (
	"testing"
)

// fakeRecordSource serves a pre-canned record set.
type fakeRecordSource struct {
	records []LocalRecord
	err     error
}

func (f *fakeRecordSource) LocalRecords(filter SyncFilter) ([]LocalRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Toy filter application: if filter.Prefix is set, keep only
	// records whose keyword starts with it.
	if filter.Prefix == "" {
		return f.records, nil
	}
	out := make([]LocalRecord, 0, len(f.records))
	for _, r := range f.records {
		if len(r.Kw) >= len(filter.Prefix) && r.Kw[:len(filter.Prefix)] == filter.Prefix {
			out = append(out, r)
		}
	}
	return out, nil
}

// SetRecordSource/RecordSource round-trip.
func TestRecordSourceAccessors(t *testing.T) {
	p := New(nil)
	if p.RecordSource() != nil {
		t.Fatal("new Protocol should have no record source")
	}
	src := &fakeRecordSource{}
	p.SetRecordSource(src)
	if p.RecordSource() != src {
		t.Error("RecordSource() should return the one just set")
	}
	p.SetRecordSource(nil)
	if p.RecordSource() != nil {
		t.Error("SetRecordSource(nil) should detach cleanly")
	}
}

// sync_begin with an attached RecordSource that has records now
// replies with sync_symbols instead of the zero-record converged
// sync_end.
func TestSyncBeginStreamSymbolsWhenRecordSourcePresent(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-recs", BitSetReconciliation)

	// Populate a small record set.
	var recs []LocalRecord
	for i := 0; i < 5; i++ {
		var r LocalRecord
		r.Pk[0] = 0xAA
		r.Kw = "linux"
		r.Ih[0] = byte(i)
		r.T = int64(i)
		recs = append(recs, r)
	}
	p.SetRecordSource(&fakeRecordSource{records: recs})

	begin := SyncBegin{TxID: 100}
	raw, _ := EncodeSyncBegin(begin)
	reply, last := captureReply()
	p.HandleMessage("peer-recs", raw, reply)

	body := last()
	if body == nil {
		t.Fatal("expected a reply")
	}
	hdr, err := peekHeader(body)
	if err != nil {
		t.Fatal(err)
	}
	if hdr.MsgType != MsgTypeSyncSymbols {
		t.Fatalf("msg_type = %d, want SyncSymbols (%d) — record source should stream", hdr.MsgType, MsgTypeSyncSymbols)
	}

	syms, err := DecodeSyncSymbols(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms.Symbols) == 0 {
		t.Error("expected ≥1 symbol in first batch")
	}

	// Session should still be registered (not released like zero-record case).
	if p.lookupSyncSession("peer-recs", 100) == nil {
		t.Error("session should remain after sync_begin when records present")
	}
}

// sync_begin with a RecordSource that returns zero matching
// records falls back to the converged sync_end path.
func TestSyncBeginZeroRecordsFromSource(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-empty", BitSetReconciliation)
	p.SetRecordSource(&fakeRecordSource{records: nil})

	begin := SyncBegin{TxID: 3}
	raw, _ := EncodeSyncBegin(begin)
	reply, last := captureReply()
	p.HandleMessage("peer-empty", raw, reply)

	body := last()
	hdr, _ := peekHeader(body)
	if hdr.MsgType != MsgTypeSyncEnd {
		t.Errorf("msg_type = %d, want SyncEnd (empty source)", hdr.MsgType)
	}
}

// RecordSource.LocalRecords returning an error is tolerated —
// handler treats it as "zero records" and falls through to
// sync_end converged.
func TestSyncBeginRecordSourceError(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-err", BitSetReconciliation)
	p.SetRecordSource(&fakeRecordSource{err: errFakeSource})

	begin := SyncBegin{TxID: 4}
	raw, _ := EncodeSyncBegin(begin)
	reply, last := captureReply()
	p.HandleMessage("peer-err", raw, reply)

	body := last()
	hdr, _ := peekHeader(body)
	if hdr.MsgType != MsgTypeSyncEnd {
		t.Errorf("msg_type = %d, want SyncEnd (source error)", hdr.MsgType)
	}
}

// sync_need against a known session with matching records in the
// source pre-populated at sync_begin returns those records in
// the sync_records reply.
func TestSyncNeedReturnsRecordsFromSource(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-need", BitSetReconciliation)

	var recs []LocalRecord
	for i := 0; i < 3; i++ {
		var r LocalRecord
		r.Pk[0] = 0xCD
		r.Kw = "ubuntu"
		r.Ih[0] = byte(i + 10)
		r.T = int64(i + 100)
		recs = append(recs, r)
	}
	p.SetRecordSource(&fakeRecordSource{records: recs})

	// Drive sync_begin first so the session loads records.
	begin := SyncBegin{TxID: 55}
	beginRaw, _ := EncodeSyncBegin(begin)
	reply1, _ := captureReply()
	p.HandleMessage("peer-need", beginRaw, reply1)

	// Request records by ID for all three stored records.
	ids := make([][]byte, 0, len(recs))
	for _, r := range recs {
		id := localRecordID(r)
		ids = append(ids, id[:])
	}
	need := SyncNeed{TxID: 55, IDs: ids}
	needRaw, _ := EncodeSyncNeed(need)
	reply2, last2 := captureReply()
	p.HandleMessage("peer-need", needRaw, reply2)

	body := last2()
	if body == nil {
		t.Fatal("expected sync_records reply")
	}
	recsReply, err := DecodeSyncRecords(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(recsReply.Records) != 3 {
		t.Errorf("records = %d, want 3", len(recsReply.Records))
	}
	if len(recsReply.Missing) != 0 {
		t.Errorf("missing = %d, want 0 (all present in source)", len(recsReply.Missing))
	}
}

// filter.Prefix is applied by fakeRecordSource; the session
// receives only prefix-matching records. A peer requesting records
// outside the prefix sees those as missing.
func TestSyncBeginAppliesFilter(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-filter", BitSetReconciliation)

	all := []LocalRecord{
		{Pk: [32]byte{1}, Kw: "linux", Ih: [20]byte{0x10}, T: 1},
		{Pk: [32]byte{1}, Kw: "linux", Ih: [20]byte{0x11}, T: 2},
		{Pk: [32]byte{1}, Kw: "ubuntu", Ih: [20]byte{0x20}, T: 3},
	}
	p.SetRecordSource(&fakeRecordSource{records: all})

	begin := SyncBegin{TxID: 9, Filter: SyncFilter{Prefix: "lin"}}
	beginRaw, _ := EncodeSyncBegin(begin)
	reply1, last1 := captureReply()
	p.HandleMessage("peer-filter", beginRaw, reply1)

	// First reply should be sync_symbols (2 records matched the prefix).
	hdr, _ := peekHeader(last1())
	if hdr.MsgType != MsgTypeSyncSymbols {
		t.Fatalf("msg_type = %d, want SyncSymbols", hdr.MsgType)
	}

	// Ask for the ubuntu record's ID — should come back missing.
	ubuntuID := localRecordID(all[2])
	need := SyncNeed{TxID: 9, IDs: [][]byte{ubuntuID[:]}}
	needRaw, _ := EncodeSyncNeed(need)
	reply2, last2 := captureReply()
	p.HandleMessage("peer-filter", needRaw, reply2)

	recsReply, err := DecodeSyncRecords(last2())
	if err != nil {
		t.Fatal(err)
	}
	if len(recsReply.Records) != 0 {
		t.Errorf("records = %d, want 0 (prefix filter should have excluded ubuntu)", len(recsReply.Records))
	}
	if len(recsReply.Missing) != 1 {
		t.Errorf("missing = %d, want 1 (ubuntu id was not in filtered set)", len(recsReply.Missing))
	}
}

// errFakeSource is a sentinel error used to simulate a
// record-source failure without importing errors for one test.
type errFakeSourceType struct{}

func (errFakeSourceType) Error() string { return "fake source error" }

var errFakeSource = errFakeSourceType{}
