package swarmsearch

import (
	"io"
	"log/slog"
	"testing"
)

// TestOnSyncNeedBuildRecordsFrameError covers the
// `frame, err := sess.BuildRecordsFrame(...); if err != nil { return }`
// arm in onSyncNeed. ApplyNeed accepts up to MaxNeedIDsPerMessage
// (1000) IDs but BuildRecordsFrame's record cap is
// MaxRecordsPerMessage (500), so a sync_need that resolves to
// >500 records reaches BuildRecordsFrame's overflow guard.
func TestOnSyncNeedBuildRecordsFrameError(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Build 501 local records with deterministic Kw values so
	// localRecordID produces 501 distinct IDs.
	records := make([]LocalRecord, MaxRecordsPerMessage+1)
	ids := make([][]byte, len(records))
	for i := range records {
		records[i] = LocalRecord{
			Kw: "kw" + string(rune('A'+(i%26))) + string(rune('0'+(i/26))),
		}
		records[i].Pk[0] = byte(i & 0xFF)
		records[i].Ih[0] = byte((i >> 8) & 0xFF)
		records[i].Sig[0] = byte((i >> 16) & 0xFF)
		id := localRecordID(records[i])
		idCopy := make([]byte, 32)
		copy(idCopy, id[:])
		ids[i] = idCopy
	}

	const peer = "p:overflow"
	const txid uint32 = 99
	sess := NewSyncSession(txid, RoleResponder, records)
	if err := sess.ApplyBegin(SyncBegin{TxID: txid, ElementSize: 32}); err != nil {
		t.Fatalf("ApplyBegin: %v", err)
	}
	p.registerSyncSession(peer, sess)

	var replyCalled bool
	reply := func([]byte) error {
		replyCalled = true
		return nil
	}
	p.onSyncNeed(peer, SyncNeed{TxID: txid, IDs: ids}, reply)
	if replyCalled {
		t.Error("BuildRecordsFrame overflow path must not invoke reply")
	}
}
