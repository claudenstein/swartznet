package swarmsearch

import (
	"errors"
	"log/slog"
	"testing"
)

// errRecordSource is a minimal RecordSource that always
// returns an error. Used to drive onSyncBegin's error-path
// in the LocalRecords call.
type errRecordSource struct{}

func (errRecordSource) LocalRecords(SyncFilter) ([]LocalRecord, error) {
	return nil, errors.New("record source: simulated failure")
}

func (errRecordSource) Add(LocalRecord) {}

// TestOnSyncBeginRecordSourceError — when the attached
// RecordSource returns an error, the handler logs it but
// continues with empty records (treated as "nothing to share")
// and emits sync_end converged. Covers the err != nil branch
// in the LocalRecords call.
func TestOnSyncBeginRecordSourceError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	p.SetRecordSource(errRecordSource{})

	var replyPayloads [][]byte
	reply := func(payload []byte) error {
		replyPayloads = append(replyPayloads, payload)
		return nil
	}

	p.onSyncBegin("p:err", SyncBegin{TxID: 1, ElementSize: 32}, reply)

	if len(replyPayloads) != 1 {
		t.Fatalf("expected 1 reply (sync_end converged), got %d", len(replyPayloads))
	}
}

// TestOnSyncBeginApplyError — bad ElementSize in the SyncBegin
// makes ApplyBegin reject the frame. The handler logs and
// returns without emitting sync_end. Covers the apply_err arm.
func TestOnSyncBeginApplyError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	var replyCalled bool
	reply := func([]byte) error {
		replyCalled = true
		return nil
	}
	// ElementSize=8 is not 32 — ApplyBegin rejects.
	p.onSyncBegin("p:apply", SyncBegin{TxID: 1, ElementSize: 8}, reply)
	if replyCalled {
		t.Error("apply_err path must not call reply")
	}
}

// TestOnSyncBeginNilSourceEmitsConverged — with no
// RecordSource attached, the handler routes through the
// zero-records branch: sync_end converged + immediate
// session release.
func TestOnSyncBeginNilSourceEmitsConverged(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	var replyPayloads [][]byte
	reply := func(payload []byte) error {
		replyPayloads = append(replyPayloads, payload)
		return nil
	}
	p.onSyncBegin("p:nil", SyncBegin{TxID: 1, ElementSize: 32}, reply)

	if len(replyPayloads) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(replyPayloads))
	}
	// After the converged emit, the session must be released so
	// follow-up frames see no entry.
	if got := p.lookupSyncSession("p:nil", 1); got != nil {
		t.Error("session should be released after zero-record converged emit")
	}
}
