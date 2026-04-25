package swarmsearch

import (
	"log/slog"
	"testing"
)

// TestOnSyncRecordsUnknownSession — receiving a SyncRecords for
// an unregistered (peer, txid) pair must short-circuit cleanly.
// This was 0% covered; the handler is reached when a peer
// resends a stale SyncRecords frame after we've torn down the
// session, or attempts a forged txid.
func TestOnSyncRecordsUnknownSession(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	// No registered session for this (peer, txid). The handler
	// should observe sess==nil and return without ingesting.
	p.onSyncRecords("9.9.9.9:1", SyncRecords{
		TxID: 12345,
		Records: []SyncRecord{
			{Pk: make([]byte, 32), Ih: make([]byte, 20), Sig: make([]byte, 64)},
		},
	})

	// No sink was set, so even a registered session would be a
	// no-op for ingestion. The point is just exercising the
	// guard branch without panicking.
}

// TestOnSyncRecordsApplyError — even when the session exists,
// ApplyRecords can fail (wrong phase, malformed counts). Exercise
// that path: register a session in PhaseIdle and feed it a
// SyncRecords frame, which is invalid for that phase.
func TestOnSyncRecordsApplyError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	sess := NewSyncSession(7, RoleResponder, nil)
	p.registerSyncSession("p:1", sess)

	// Frame for an idle responder — ApplyRecords requires
	// PhaseSendingRecords for the responder; PhaseIdle is the
	// wrong phase, so apply returns an error and we exit via
	// the apply_err branch.
	p.onSyncRecords("p:1", SyncRecords{TxID: 7})
}

// TestOnSyncEndUnknownSession — the handler must not panic when
// the session was already released or never existed. The
// release call afterwards is also a no-op.
func TestOnSyncEndUnknownSession(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	p.onSyncEnd("never-existed:1", SyncEnd{TxID: 99, Status: "ok"})
}

// TestOnSyncEndKnownSessionReleases — register a session, call
// onSyncEnd, verify the session is gone afterwards. This is the
// production happy path.
func TestOnSyncEndKnownSessionReleases(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	sess := NewSyncSession(123, RoleResponder, nil)
	p.registerSyncSession("peer-A:9", sess)
	if got := p.lookupSyncSession("peer-A:9", 123); got == nil {
		t.Fatal("registerSyncSession didn't store")
	}
	p.onSyncEnd("peer-A:9", SyncEnd{TxID: 123, Status: "ok"})
	if got := p.lookupSyncSession("peer-A:9", 123); got != nil {
		t.Error("onSyncEnd didn't release the session")
	}
}
