package swarmsearch

import (
	"log/slog"
	"testing"
)

// Each of sendSyncEnd / sendSyncRecords / sendSyncSymbols is a
// 3-line wrapper: encode → log + drop on error → forward to
// reply. The happy path is exercised by the end-to-end sync
// scenario; these tests fill in the encode-error arm by
// passing malformed inputs that the matching Encode function
// rejects.

func TestSendSyncRecordsDropsOnEncodeError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	var replyCalled bool
	reply := func([]byte) error {
		replyCalled = true
		return nil
	}
	// Bad pk length — EncodeSyncRecords rejects pre-marshal.
	bad := SyncRecord{Pk: make([]byte, 16), Ih: make([]byte, 20), Sig: make([]byte, 64)}
	p.sendSyncRecords(reply, SyncRecords{TxID: 1, Records: []SyncRecord{bad}})
	if replyCalled {
		t.Error("encode-error path must not call reply")
	}
}

func TestSendSyncSymbolsDropsOnEncodeError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	var replyCalled bool
	reply := func([]byte) error {
		replyCalled = true
		return nil
	}
	// Empty Symbols — EncodeSyncSymbols rejects (must have ≥1).
	p.sendSyncSymbols(reply, SyncSymbols{TxID: 1, Symbols: nil})
	if replyCalled {
		t.Error("encode-error path must not call reply")
	}
}

// sendSyncEnd has no encode-error branch we can trigger from
// public input — EncodeSyncEnd accepts every SyncEnd shape.
// Cover the nil-reply branch instead so the function's other
// guard at least executes.
func TestSendSyncEndNilReplyNoOp(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	// nil reply: function must not panic, must not deref.
	p.sendSyncEnd(nil, SyncEnd{TxID: 1, Status: SyncStatusConverged})
}
