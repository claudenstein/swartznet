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
	}, nil)

	// No sink was set, so even a registered session would be a
	// no-op for ingestion. The point is just exercising the
	// guard branch without panicking.
}

// TestOnSyncRecordsApplyError covers the
// `records, err := sess.ApplyRecords(m); if err != nil` arm.
// Register a session driven to PhaseNeeded, then feed
// onSyncRecords a frame whose record has a wrong-length Pk so
// ApplyRecords' "record[N] bad sizes" check fires; onSyncRecords
// must fail closed — sync_end aborted, session released, peer
// charged — without invoking the sink.
func TestOnSyncRecordsApplyError(t *testing.T) {
	t.Parallel()
	p := New(slog.Default())
	sess := NewSyncSession(7, RoleInitiator, nil)
	if _, err := sess.Begin(SyncFilter{}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := sess.NeedFrame(nil); err != nil {
		t.Fatalf("NeedFrame: %v", err)
	}
	p.registerSyncSession("p:1", sess)
	// Record with Pk of length 5 — ApplyRecords requires 32.
	// TxID matches the session so we get past the txid guard
	// and into the size-check loop.
	p.onSyncRecords("p:1", SyncRecords{
		TxID: 7,
		Records: []SyncRecord{
			{Pk: make([]byte, 5), Ih: make([]byte, 20), Sig: make([]byte, 64)},
		},
	}, nil)
	if p.lookupSyncSession("p:1", 7) != nil {
		t.Error("session should be released after ApplyRecords violation")
	}
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
