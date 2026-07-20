package swarmsearch

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSearcher returns a fixed hit set.
type fakeSearcher struct {
	total int
	hits  []LocalHit
	err   error
}

func (f *fakeSearcher) SearchLocal(string, int) (int, []LocalHit, error) {
	return f.total, f.hits, f.err
}

// harness wires N protocols so that SendExtension(token, frame) delivers the
// frame to the addressed peer's HandleMessage, with a reply closure that routes
// the response back to the sender. This models the engine transport in-memory.
type harness struct {
	peers map[string]*Protocol
}

func newHarness() *harness { return &harness{peers: make(map[string]*Protocol)} }

// transportFor returns a Transport whose SendExtension delivers to the target
// protocol as if from `selfAddr`.
type htTransport struct {
	h        *harness
	selfAddr string
}

func (t *htTransport) SendExtension(peer PeerToken, frame []byte) error {
	target := t.h.peers[peer.Addr()]
	if target == nil {
		return nil
	}
	self := t.h.peers[t.selfAddr]
	reply := func(payload []byte) error {
		// deliver the reply back to us, from the target's addr
		self.HandleMessage(peer.Addr(), payload, nil)
		return nil
	}
	target.HandleMessage(t.selfAddr, frame, reply)
	return nil
}

// connect makes a and b mutually aware + capable (both advertised sn_search).
//
// NotePeerAdded MUST run for both sides before either OnRemoteHandshake: the
// latter enqueues an outbound peer_announce on an async worker, and
// handlePeerAnnounce drops any announce for a peer it has no entry for (the
// anti-zombie guard in handler.go). In production that entry always exists —
// NotePeerAdded is called synchronously on connection add, before the LTEP
// handshake that triggers any announce. If connect() skipped it and relied on
// OnRemoteHandshake to create the entry, A's async announce could reach B in the
// window between the two OnRemoteHandshake calls — before B.peers[A] exists — and
// B would silently drop it, so B never learns A's capabilities. That window is
// only hit under true parallelism + scheduler pressure, which is exactly why it
// surfaced as a flaky CI failure (loaded -race runner) and never locally.
func (h *harness) connect(a, b string) {
	pa, pb := h.peers[a], h.peers[b]
	pa.SetTransport(&htTransport{h: h, selfAddr: a})
	pb.SetTransport(&htTransport{h: h, selfAddr: b})
	pa.NotePeerAdded(b)
	pb.NotePeerAdded(a)
	pa.OnRemoteHandshake(b, true, 1)
	pb.OnRemoteHandshake(a, true, 1)
}

func fullShare() func() Capabilities {
	return func() Capabilities {
		return Capabilities{Sharing: ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true}, Services: 0x2ED}
	}
}

// TestQueryResultRoundTrip: A queries B and gets B's hits.
func TestQueryResultRoundTrip(t *testing.T) {
	h := newHarness()
	a, b := New(testLog()), New(testLog())
	defer a.Close()
	defer b.Close()
	h.peers["A"], h.peers["B"] = a, b
	b.SetCapabilitySource(fullShare())
	b.SetSearcher(&fakeSearcher{total: 1, hits: []LocalHit{{
		DocType: "torrent", InfoHash: "1111111111111111111111111111111111111111",
		Name: "ubuntu.iso", SizeBytes: 100, Score: 0.9,
	}}})
	h.connect("A", "B")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := a.Query(ctx, QueryRequest{Q: "ubuntu"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Asked != 1 || resp.Responded != 1 || resp.Rejected != 0 {
		t.Fatalf("counts asked=%d responded=%d rejected=%d", resp.Asked, resp.Responded, resp.Rejected)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].Name != "ubuntu.iso" {
		t.Fatalf("hits = %+v", resp.Hits)
	}
	if resp.Hits[0].Sources[0] != "B" {
		t.Errorf("source = %v, want B", resp.Hits[0].Sources)
	}
}

// TestScopeRejectCode2: a scope-"c" query to a ContentHits=0 responder → reject 2.
func TestScopeRejectCode2(t *testing.T) {
	h := newHarness()
	a, b := New(testLog()), New(testLog())
	defer a.Close()
	defer b.Close()
	h.peers["A"], h.peers["B"] = a, b
	// B shares full-local + file, but NOT content.
	b.SetCapabilitySource(func() Capabilities {
		return Capabilities{Sharing: ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: false}}
	})
	b.SetSearcher(&fakeSearcher{})
	h.connect("A", "B")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, _ := a.Query(ctx, QueryRequest{Q: "ubuntu", Scope: "c"})
	if resp.Rejected != 1 {
		t.Fatalf("scope-c to a content-less peer should reject: %+v", resp)
	}
}

// TestScopeCBeforeF: a node lacking both f and c returns _c for scope "fc".
func TestScopeCBeforeF(t *testing.T) {
	caps := ltepwire.Sharing{ShareLocal: 2, FileHits: false, ContentHits: false}
	code, reason, rejected := unsupportedScope("fc", caps)
	if !rejected || code != ltepwire.RejectUnsupportedScope || reason != "unsupported_scope_c" {
		t.Fatalf("scope fc → code=%d reason=%q rejected=%v, want reject unsupported_scope_c", code, reason, rejected)
	}
}

// TestShareLocalOneFailsClosed: ShareLocal==1 rejects (no swarm-membership filter).
func TestShareLocalOneFailsClosed(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	p.SetCapabilitySource(func() Capabilities {
		return Capabilities{Sharing: ltepwire.Sharing{ShareLocal: 1, FileHits: true, ContentHits: true}}
	})
	p.SetSearcher(&fakeSearcher{total: 1})
	var replied *ltepwire.Reject
	reply := captureReject(t, &replied)
	q, _ := ltepwire.EncodeQuery(ltepwire.Query{TxID: 1, Q: "ubuntu"})
	p.handleQuery("peer", q, reply)
	if replied == nil || replied.Code != ltepwire.RejectShuttingDown {
		t.Fatalf("ShareLocal==1 should reject 4, got %+v", replied)
	}
}

// TestBadShapeQueryCharged is the §6 defect-(a) fix: a valid-bencode wrong-shape
// query is charged ScoreBadBencode and produces no reply.
func TestBadShapeQueryCharged(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	p.SetCapabilitySource(fullShare())
	p.SetSearcher(&fakeSearcher{})
	// A dict with msg_type=0 but `q` as an integer (wrong shape; passes peek).
	bad := []byte("d8:msg_typei0e1:qi5e4:txidi1ee")
	replyCalled := false
	p.HandleMessage("peer", bad, func([]byte) error { replyCalled = true; return nil })
	if replyCalled {
		t.Error("a wrong-shape query must not produce a reply")
	}
	if p.ban.Score("peer") != ScoreBadBencode {
		t.Errorf("score = %d, want %d (ScoreBadBencode)", p.ban.Score("peer"), ScoreBadBencode)
	}
}

// TestResultFromUnaskedPeerCharged: a result with a live txid but from a peer
// not in the asked set is charged and dropped.
func TestResultFromUnaskedPeerCharged(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	pq := &pendingQuery{txid: 7, asked: map[string]bool{"asked-peer": true}, results: make(chan incomingResult, 1)}
	p.registerPending(pq)
	frame, _ := ltepwire.EncodeResult(ltepwire.Result{TxID: 7, Hits: []ltepwire.Hit{}})
	p.HandleMessage("evil-peer", frame, nil)
	if p.ban.Score("evil-peer") != ScoreUnexpectedMessage {
		t.Errorf("unasked result score = %d, want %d", p.ban.Score("evil-peer"), ScoreUnexpectedMessage)
	}
	if len(pq.results) != 0 {
		t.Error("result from an unasked peer must be dropped")
	}
}

// TestStaleTxIDCharged: a result for no live pending is charged.
func TestStaleTxIDCharged(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	frame, _ := ltepwire.EncodeResult(ltepwire.Result{TxID: 999, Hits: []ltepwire.Hit{}})
	p.HandleMessage("peer", frame, nil)
	if p.ban.Score("peer") != ScoreStaleTxID {
		t.Errorf("stale txid score = %d, want %d", p.ban.Score("peer"), ScoreStaleTxID)
	}
}

// TestMalformedChargedBeforeLookup: a malformed result is charged even when its
// txid matches no pending (charge happens before the lookup).
func TestMalformedChargedBeforeLookup(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	// Total>0 with zero hits = malformed.
	frame, _ := ltepwire.EncodeResult(ltepwire.Result{TxID: 3, Total: 5, Hits: []ltepwire.Hit{}})
	p.routeResult("peer", mustDecodeResult(t, frame))
	if p.ban.Score("peer") != ScoreMalformedResult {
		t.Errorf("malformed score = %d, want %d", p.ban.Score("peer"), ScoreMalformedResult)
	}
}

// TestPeerAnnounceStoresServices + all-zero pk rejected. NotePeerAdded precedes
// the announce, mirroring production: PeerConnAdded (→ NotePeerAdded) fires
// synchronously before any inbound frame is dispatched, so the entry always
// exists when a peer_announce is processed (handlePeerAnnounce updates it; it
// never creates one, so a post-close announce cannot resurrect a zombie).
func TestPeerAnnounceStoresServices(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	p.NotePeerAdded("peer")
	// all-zero pk → pubkey not stored, rest processed.
	frame, _ := ltepwire.EncodePeerAnnounce(ltepwire.PeerAnnounce{Services: 0x2ED, Pk: make([]byte, 32)})
	p.HandleMessage("peer", frame, nil)
	p.mu.Lock()
	ps := p.peers["peer"]
	p.mu.Unlock()
	if ps == nil || ps.Services != 0x2ED {
		t.Fatalf("services not stored: %+v", ps)
	}
	if ps.hasPubkey {
		t.Error("all-zero pk must be rejected")
	}
}

// TestNoAnnounceStillAnswered: a query is answered even if the peer never sent a
// peer_announce (absence of announce = services 0, still answered).
func TestNoAnnounceStillAnswered(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	p.SetCapabilitySource(fullShare())
	p.SetSearcher(&fakeSearcher{total: 1, hits: []LocalHit{{DocType: "torrent", InfoHash: "1111111111111111111111111111111111111111", Name: "x"}}})
	var res *ltepwire.Result
	reply := captureResult(t, &res)
	q, _ := ltepwire.EncodeQuery(ltepwire.Query{TxID: 1, Q: "ubuntu"})
	// No peer_announce ever processed for "peer".
	p.HandleMessage("peer", q, reply)
	if res == nil || len(res.Hits) != 1 {
		t.Fatalf("query answered without a prior announce should return hits: %+v", res)
	}
}

// TestRateBucketStartsFull: 10 immediate queries pass, the 11th is limited.
func TestRateBucketStartsFull(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	p.SetCapabilitySource(fullShare())
	p.SetSearcher(&fakeSearcher{})
	q, _ := ltepwire.EncodeQuery(ltepwire.Query{TxID: 1, Q: "ubuntu"})
	rejects := 0
	for i := 0; i < 11; i++ {
		var rj *ltepwire.Reject
		p.HandleMessage("peer", q, captureReject(t, &rj))
		if rj != nil && rj.Code == ltepwire.RejectRateLimited {
			rejects++
		}
	}
	if rejects != 1 {
		t.Errorf("burst 10 → want exactly 1 rate-limit reject, got %d", rejects)
	}
}

// TestBanThreshold: enough bad frames ban the peer.
func TestBanThreshold(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	for i := 0; i < 5; i++ { // 5 × 20 = 100
		p.HandleMessage("peer", []byte("not bencode"), nil)
	}
	if !p.ban.IsBanned("peer") {
		t.Errorf("5×ScoreBadBencode should reach BanThreshold %d", BanThreshold)
	}
}

// TestForgetKeepsBanned: OnPeerClosed forgets a clean peer but keeps a ban.
func TestForgetKeepsBanned(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	for i := 0; i < 5; i++ {
		p.HandleMessage("bad", []byte("garbage"), nil)
	}
	p.OnPeerClosed("bad")
	if !p.ban.IsBanned("bad") {
		t.Error("a ban must survive OnPeerClosed")
	}
}

// recordingTransport records every SendExtension addr (deterministic silence
// proof).
type recordingTransport struct{ sent []string }

func (r *recordingTransport) SendExtension(peer PeerToken, _ []byte) error {
	r.sent = append(r.sent, peer.Addr())
	return nil
}

// TestVanillaPeerNeverQueried is the deterministic CI silence assertion: a peer
// whose LTEP m dict omitted sn_search (supported=false) is never a query
// target and never receives a frame.
func TestVanillaPeerNeverQueried(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	rt := &recordingTransport{}
	p.SetTransport(rt)
	// A peer connected but advertised NO sn_search (empty m dict).
	p.OnRemoteHandshake("vanilla", false, 0)
	if p.CapablePeerCount() != 0 {
		t.Fatalf("a non-advertising peer must not count as capable: %d", p.CapablePeerCount())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := p.Query(ctx, QueryRequest{Q: "ubuntu"})
	if err != ErrNoCapablePeers {
		t.Fatalf("query with only a vanilla peer = %v, want ErrNoCapablePeers", err)
	}
	if len(rt.sent) != 0 {
		t.Errorf("frames sent to a vanilla peer: %v (want none)", rt.sent)
	}
}

// TestOnePeerCannotFloodResults is the review fix: a single asked peer sending
// N result frames counts as ONE responder (not N), so it cannot end collection
// early, inflate Responded, or monopolize the merged set.
func TestOnePeerCannotFloodResults(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	asked := map[string]bool{"flooder": true, "honest": true}
	pq := &pendingQuery{txid: 5, asked: asked, results: make(chan incomingResult, 2), responded: map[string]bool{}}
	p.registerPending(pq)
	// Flooder sends two valid results with the live txid.
	f1, _ := ltepwire.EncodeResult(ltepwire.Result{TxID: 5, Hits: []ltepwire.Hit{{IH: make([]byte, 20), N: "a"}}})
	p.HandleMessage("flooder", f1, nil)
	p.HandleMessage("flooder", f1, nil)
	if len(pq.results) != 1 {
		t.Fatalf("flooder occupied %d result slots, want 1 (per-peer dedup)", len(pq.results))
	}
	if p.ban.Score("flooder") != 0 {
		t.Errorf("a benign duplicate must not be charged, score=%d", p.ban.Score("flooder"))
	}
}

// TestReconciliationSyncFrameCharged: since Slice 7 does NOT advertise bit 9, a
// sync frame is anomalous and IS charged (the gate is valid now).
func TestReconciliationSyncFrameCharged(t *testing.T) {
	p := New(testLog())
	defer p.Close()
	frame, _ := ltepwire.EncodeQuery(ltepwire.Query{TxID: 1, Q: "x"}) // valid header
	// Re-stamp msg_type to a sync type by hand-building the dict.
	sync := []byte("d8:msg_typei4e4:txidi9ee")
	_ = frame
	p.HandleMessage("peer", sync, nil)
	if p.ban.Score("peer") != ScoreUnexpectedMessage {
		t.Errorf("sync frame score = %d, want %d", p.ban.Score("peer"), ScoreUnexpectedMessage)
	}
}

// ---- reply-capture helpers ----

func captureReject(t *testing.T, out **ltepwire.Reject) ReplyFunc {
	t.Helper()
	return func(payload []byte) error {
		if mt, _ := ltepwire.PeekMsgType(payload); mt == ltepwire.MsgTypeReject {
			rj, err := ltepwire.DecodeReject(payload)
			if err == nil {
				*out = &rj
			}
		}
		return nil
	}
}
func captureResult(t *testing.T, out **ltepwire.Result) ReplyFunc {
	t.Helper()
	return func(payload []byte) error {
		if mt, _ := ltepwire.PeekMsgType(payload); mt == ltepwire.MsgTypeResult {
			r, err := ltepwire.DecodeResult(payload)
			if err == nil {
				*out = &r
			}
		}
		return nil
	}
}
func mustDecodeResult(t *testing.T, frame []byte) ltepwire.Result {
	t.Helper()
	r, err := ltepwire.DecodeResult(frame)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
