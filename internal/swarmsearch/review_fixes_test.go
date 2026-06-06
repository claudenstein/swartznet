package swarmsearch

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestApplySymbolsIndexMismatchAborts proves the RIBLT desync guard:
// a sync_symbols frame whose on-wire Index doesn't match the
// decoder's expected next position is rejected instead of silently
// appending at the wrong offset (which would corrupt the diff).
func TestApplySymbolsIndexMismatchAborts(t *testing.T) {
	t.Parallel()
	recs := make([]LocalRecord, 5)
	for i := range recs {
		recs[i] = makeLocalRecord(fmt.Sprintf("kw-%d", i), byte(i), int64(i))
	}
	ini := NewSyncSession(1, RoleInitiator, recs)
	if _, err := ini.Begin(SyncFilter{}); err != nil {
		t.Fatal(err)
	}

	good := SyncSymbols{
		TxID:    1,
		Index:   0, // first frame must start at 0
		Symbols: []SyncSymbol{{Count: 1, DataXOR: make([]byte, 32)}},
	}
	if err := ini.ApplySymbols(good); err != nil {
		t.Fatalf("first frame at index 0 must apply: %v", err)
	}

	// Now the decoder expects index 1. Feed a frame claiming index 5
	// (as if frames 1..4 were dropped). It must be rejected.
	desynced := SyncSymbols{
		TxID:    1,
		Index:   5,
		Symbols: []SyncSymbol{{Count: 1, DataXOR: make([]byte, 32)}},
	}
	if err := ini.ApplySymbols(desynced); err == nil {
		t.Fatal("ApplySymbols must reject a frame whose Index skips ahead (desync)")
	}
}

// TestSyncSessionPerPeerCap proves onSyncBegin fails closed: a peer
// that opens more than MaxSyncSessionsPerPeer concurrent sessions is
// rejected and charged misbehavior rather than accumulating RIBLT
// state without bound.
func TestSyncSessionPerPeerCap(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "flooder:1", BitSetReconciliation)
	reply, _ := captureReply()

	// Fill the cap with distinct txids. A zero-record source means
	// each session immediately emits sync_end converged and releases
	// itself, so to actually accumulate sessions we attach a source
	// that returns a record (forcing the symbol-streaming path that
	// leaves the session registered).
	p.SetRecordSource(staticRecordSource{recs: []LocalRecord{
		makeLocalRecord("kw", 1, 1),
	}})

	for i := uint32(0); i < MaxSyncSessionsPerPeer; i++ {
		p.onSyncBegin("flooder:1", SyncBegin{TxID: i, ElementSize: 32}, reply)
	}
	if got := p.syncSessionCount("flooder:1", 0xFFFFFFFF); got != MaxSyncSessionsPerPeer {
		t.Fatalf("expected %d live sessions, got %d", MaxSyncSessionsPerPeer, got)
	}

	scoreBefore := p.MisbehaviorScore("flooder:1")
	// One past the cap: must be rejected, no new session, charged.
	p.onSyncBegin("flooder:1", SyncBegin{TxID: 9999, ElementSize: 32}, reply)
	if got := p.syncSessionCount("flooder:1", 0xFFFFFFFF); got != MaxSyncSessionsPerPeer {
		t.Fatalf("session over cap was admitted: count=%d", got)
	}
	if p.MisbehaviorScore("flooder:1") <= scoreBefore {
		t.Fatal("exceeding the per-peer session cap must charge misbehavior")
	}
}

// TestReapStaleSyncSessions proves abandoned sessions reach a
// terminal state: a session older than the stale threshold is
// reaped, removed, and gets a sync_end aborted frame.
func TestReapStaleSyncSessions(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "ghost:1", BitSetReconciliation)

	// Register a session and backdate its creation time past the
	// stale threshold.
	sess := NewSyncSession(7, RoleResponder, []LocalRecord{makeLocalRecord("kw", 1, 1)})
	sess.createdAt = time.Now().Add(-SyncSessionStaleAfter - time.Second)
	p.registerSyncSession("ghost:1", sess)

	reply, last := captureReply()
	p.reapStaleSyncSessions("ghost:1", reply)

	if p.lookupSyncSession("ghost:1", 7) != nil {
		t.Fatal("stale session should have been reaped")
	}
	body := last()
	if body == nil {
		t.Fatal("reaper must emit a sync_end frame for the reaped session")
	}
	end, err := DecodeSyncEnd(body)
	if err != nil {
		t.Fatalf("reaped sync_end did not decode: %v", err)
	}
	if end.Status != SyncStatusAborted {
		t.Fatalf("reaped session status = %q, want %q", end.Status, SyncStatusAborted)
	}

	// A fresh session must NOT be reaped.
	fresh := NewSyncSession(8, RoleResponder, []LocalRecord{makeLocalRecord("kw2", 2, 2)})
	p.registerSyncSession("ghost:1", fresh)
	p.reapStaleSyncSessions("ghost:1", reply)
	if p.lookupSyncSession("ghost:1", 8) == nil {
		t.Fatal("fresh session must survive the reaper")
	}
}

// TestShareLocalOneFailsClosed proves ShareLocal==1 rejects queries
// rather than leaking the full local index (no swarm-aware searcher
// exists yet).
func TestShareLocalOneFailsClosed(t *testing.T) {
	p := New(nil)
	p.SetCapabilities(Capabilities{ShareLocal: 1, FileHits: 1, ContentHits: 1})
	p.SetSearcher(fakeSearcher{})

	q, err := EncodeQuery(Query{TxID: 3, Q: "hello world", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	reply, last := captureReply()
	p.handleQuery("peer:1", q, reply)

	body := last()
	if body == nil {
		t.Fatal("ShareLocal==1 must send a reject, not silence")
	}
	rj, err := DecodeReject(body)
	if err != nil {
		t.Fatalf("expected a reject frame, decode failed: %v", err)
	}
	if rj.Code != RejectShuttingDown {
		t.Fatalf("ShareLocal==1 reject code = %d, want %d", rj.Code, RejectShuttingDown)
	}
}

// TestStartFeelerIdempotent proves repeated StartFeeler calls don't
// launch duplicate goroutines: feelerRunning is set once and stays
// set while the loop runs.
func TestStartFeelerIdempotent(t *testing.T) {
	p := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.StartFeeler(ctx, time.Hour) // long interval → loop just blocks on the ticker
	if !p.feelerRunning.Load() {
		t.Fatal("first StartFeeler should mark the feeler running")
	}
	// Second call must be a no-op; the flag stays true and no second
	// goroutine is launched (we can't count goroutines deterministically
	// here, but the CAS guard is what the no-op contract relies on).
	p.StartFeeler(ctx, time.Hour)
	if !p.feelerRunning.Load() {
		t.Fatal("flag must remain set after a redundant StartFeeler")
	}

	cancel()
	// After cancellation the guard releases so a restart is possible.
	deadline := time.Now().Add(2 * time.Second)
	for p.feelerRunning.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.feelerRunning.Load() {
		t.Fatal("feeler guard should release after context cancellation")
	}
}

// TestRouteResultStaleTxIDCharged proves a Result that matches no
// pending query charges ScoreStaleTxID.
func TestRouteResultStaleTxIDCharged(t *testing.T) {
	p := New(nil)
	r := Result{TxID: 123, Hits: []Hit{{IH: make([]byte, 20)}}}
	p.routeResult("stale:1", r)
	if p.MisbehaviorScore("stale:1") != ScoreStaleTxID {
		t.Fatalf("stale result txid score = %d, want %d",
			p.MisbehaviorScore("stale:1"), ScoreStaleTxID)
	}
}

// TestRouteRejectStaleTxIDCharged is the mirror for reject frames.
func TestRouteRejectStaleTxIDCharged(t *testing.T) {
	p := New(nil)
	p.routeReject("stale:2", Reject{TxID: 456})
	if p.MisbehaviorScore("stale:2") != ScoreStaleTxID {
		t.Fatalf("stale reject txid score = %d, want %d",
			p.MisbehaviorScore("stale:2"), ScoreStaleTxID)
	}
}

// TestRouteResultMalformedCharged proves a semantically-garbage
// Result (bad infohash length, or Total>0 with no hits) charges
// ScoreMalformedResult.
func TestRouteResultMalformedCharged(t *testing.T) {
	cases := []struct {
		name string
		r    Result
	}{
		{"short_infohash", Result{TxID: 1, Hits: []Hit{{IH: []byte{0x01, 0x02}}}}},
		{"total_without_hits", Result{TxID: 2, Total: 5, Hits: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New(nil)
			p.routeResult("bad:1", tc.r)
			if p.MisbehaviorScore("bad:1") != ScoreMalformedResult {
				t.Fatalf("score = %d, want %d", p.MisbehaviorScore("bad:1"), ScoreMalformedResult)
			}
		})
	}
}

// TestMergeResponsesDedupsPerPeer proves a peer repeating the same
// infohash in one Result is counted once: its Rank is not summed
// twice and its address appears once in Sources.
func TestMergeResponsesDedupsPerPeer(t *testing.T) {
	t.Parallel()
	ih := make([]byte, 20)
	ih[0] = 0xAB
	responses := []incomingResult{
		{
			peer: "dupe:1",
			result: Result{Hits: []Hit{
				{IH: ih, Rank: 300, N: "name"},
				{IH: ih, Rank: 300}, // same infohash again
				{IH: ih, Rank: 300}, // and again
			}},
		},
	}
	merged := mergeResponses(responses)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged hit, got %d", len(merged))
	}
	if merged[0].Score != 300 {
		t.Fatalf("score double-counted: got %d, want 300", merged[0].Score)
	}
	if len(merged[0].Sources) != 1 {
		t.Fatalf("Sources padded with repeats: %v", merged[0].Sources)
	}
}

// staticRecordSource returns a fixed record set regardless of filter.
type staticRecordSource struct{ recs []LocalRecord }

func (s staticRecordSource) LocalRecords(SyncFilter) ([]LocalRecord, error) {
	return s.recs, nil
}

// fakeSearcher returns a single torrent hit so the ShareLocal test
// would observe a leak if the fail-closed guard were missing.
type fakeSearcher struct{}

func (fakeSearcher) SearchLocal(string, int) (int, []LocalHit, error) {
	return 1, []LocalHit{{DocType: "torrent", InfoHash: "aabbccddeeff00112233445566778899aabbccdd", Name: "leak"}}, nil
}
