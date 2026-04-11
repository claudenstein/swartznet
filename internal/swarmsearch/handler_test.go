package swarmsearch_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// fakeSearcher is a minimal LocalSearcher used by handler tests. It
// returns a fixed set of hits for any non-empty query, and records the
// last query string so tests can assert dispatch happened.
type fakeSearcher struct {
	mu        sync.Mutex
	lastQuery string
	hits      []swarmsearch.LocalHit
	total     int
	err       error
}

func (f *fakeSearcher) SearchLocal(q string, limit int) (int, []swarmsearch.LocalHit, error) {
	f.mu.Lock()
	f.lastQuery = q
	f.mu.Unlock()
	if f.err != nil {
		return 0, nil, f.err
	}
	return f.total, f.hits, nil
}

// captureReply is a ReplyFunc that records every payload it was asked
// to send. It is the test double for the per-message reply closure
// the engine builds around *torrent.PeerConn.WriteExtendedMessage.
type captureReply struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (c *captureReply) fn() swarmsearch.ReplyFunc {
	return func(payload []byte) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.msgs = append(c.msgs, payload)
		return nil
	}
}

func (c *captureReply) last() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return nil, false
	}
	return c.msgs[len(c.msgs)-1], true
}

func newSilentProtocol() *swarmsearch.Protocol {
	return swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestHandleInboundRateLimit exercises the M12f rate limiter
// end-to-end through the Protocol's HandleMessage entry point:
// a burst of queries from one peer must eventually hit a
// RejectRateLimited reply, while a different peer is unaffected.
func TestHandleInboundRateLimit(t *testing.T) {
	t.Parallel()
	p := newSilentProtocol()
	// Tight limit: burst 3, slow refill so the test doesn't
	// need to wait for a top-up.
	p.SetRateLimit(swarmsearch.RateLimit{
		QueriesPerSecond: 0.01,
		Burst:            3,
	})
	p.SetSearcher(&fakeSearcher{total: 0, hits: nil})

	reply := &captureReply{}
	const peer = "10.9.9.9:6881"
	for i := 0; i < 3; i++ {
		payload, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: uint32(i + 1), Q: "foo"})
		if err != nil {
			t.Fatal(err)
		}
		p.HandleMessage(peer, payload, reply.fn())
	}

	// 4th query must return a RejectRateLimited.
	payload, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 4, Q: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage(peer, payload, reply.fn())
	msg, ok := reply.last()
	if !ok {
		t.Fatal("no reply captured for over-quota query")
	}
	rej, err := swarmsearch.DecodeReject(msg)
	if err != nil {
		t.Fatalf("reply not a Reject: %v (msg=%x)", err, msg)
	}
	if rej.Code != swarmsearch.RejectRateLimited {
		t.Errorf("reject code = %d, want RejectRateLimited (%d)", rej.Code, swarmsearch.RejectRateLimited)
	}
	if rej.TxID != 4 {
		t.Errorf("reject txid = %d, want 4", rej.TxID)
	}

	// A different peer's first query must still succeed.
	otherReply := &captureReply{}
	payload2, err := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 100, Q: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage("10.9.9.8:6881", payload2, otherReply.fn())
	msg2, ok := otherReply.last()
	if !ok {
		t.Fatal("isolated peer got no reply")
	}
	// It should be a Result (total=0), not a Reject. We don't
	// need to decode it — confirming it isn't the reject tag is
	// enough since we already tested happy-path decode above.
	if _, err := swarmsearch.DecodeReject(msg2); err == nil {
		t.Errorf("isolated peer got a Reject instead of a Result")
	}
}

func TestHandleInboundQueryAnsweredFromLocalIndex(t *testing.T) {
	t.Parallel()
	p := newSilentProtocol()

	searcher := &fakeSearcher{
		total: 3,
		hits: []swarmsearch.LocalHit{
			{
				DocType:   "torrent",
				InfoHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Name:      "Ubuntu 24.04 ISO",
				SizeBytes: 6 * 1024 * 1024 * 1024,
				Seeders:   100,
				Score:     0.9,
			},
			{
				DocType:   "content",
				InfoHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				FileIndex: 2,
				FilePath:  "README.md",
				Score:     0.8,
			},
		},
	}
	reply := &captureReply{}
	p.SetSearcher(searcher)

	q := swarmsearch.Query{TxID: 7, Q: "ubuntu", Limit: 20}
	payload, err := swarmsearch.EncodeQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage("10.0.0.1:6881", payload, reply.fn())

	if searcher.lastQuery != "ubuntu" {
		t.Errorf("searcher received %q, want %q", searcher.lastQuery, "ubuntu")
	}
	msg, ok := reply.last()
	if !ok {
		t.Fatal("no outbound message captured")
	}
	result, err := swarmsearch.DecodeResult(msg)
	if err != nil {
		t.Fatalf("reply payload is not a Result: %v", err)
	}
	if result.TxID != 7 {
		t.Errorf("reply TxID = %d, want 7", result.TxID)
	}
	if result.Total != 3 {
		t.Errorf("reply Total = %d, want 3", result.Total)
	}
	// The torrent hit and the content hit share an infohash, so the
	// handler must emit a single Hit with one Matches entry.
	if len(result.Hits) != 1 {
		t.Fatalf("len(Hits) = %d, want 1", len(result.Hits))
	}
	if len(result.Hits[0].Matches) != 1 {
		t.Errorf("matches len = %d, want 1", len(result.Hits[0].Matches))
	}
	if result.Hits[0].N != "Ubuntu 24.04 ISO" {
		t.Errorf("hit name = %q, want Ubuntu 24.04 ISO", result.Hits[0].N)
	}
}

func TestHandleInboundQueryTooShortRejects(t *testing.T) {
	t.Parallel()
	p := newSilentProtocol()
	reply := &captureReply{}
	p.SetSearcher(&fakeSearcher{})

	q := swarmsearch.Query{TxID: 1, Q: "a"}
	payload, _ := swarmsearch.EncodeQuery(q)
	p.HandleMessage("10.0.0.1:6881", payload, reply.fn())

	msg, ok := reply.last()
	if !ok {
		t.Fatal("no reject sent")
	}
	rj, err := swarmsearch.DecodeReject(msg)
	if err != nil {
		t.Fatalf("reply is not a reject: %v", err)
	}
	if rj.Code != swarmsearch.RejectQueryTooBroad {
		t.Errorf("reject code = %d, want %d", rj.Code, swarmsearch.RejectQueryTooBroad)
	}
}

func TestHandleInboundQueryNoSearcherRejects(t *testing.T) {
	t.Parallel()
	p := newSilentProtocol()
	reply := &captureReply{}
	// No SetSearcher: we stay with the default nil.

	payload, _ := swarmsearch.EncodeQuery(swarmsearch.Query{TxID: 1, Q: "ubuntu"})
	p.HandleMessage("10.0.0.1:6881", payload, reply.fn())

	msg, ok := reply.last()
	if !ok {
		t.Fatal("no reject sent for missing searcher")
	}
	rj, err := swarmsearch.DecodeReject(msg)
	if err != nil {
		t.Fatalf("reply is not a reject: %v", err)
	}
	if rj.Code != swarmsearch.RejectShuttingDown {
		t.Errorf("reject code = %d, want %d", rj.Code, swarmsearch.RejectShuttingDown)
	}
}

func TestHandleGarbagePayloadDoesNothing(t *testing.T) {
	t.Parallel()
	p := newSilentProtocol()
	reply := &captureReply{}

	// Should not panic, should not send anything.
	p.HandleMessage("10.0.0.1:6881", []byte("not a bencoded message"), reply.fn())

	if _, ok := reply.last(); ok {
		t.Error("garbage input produced an outbound message")
	}
}

func TestHandleInboundResultLogsOnly(t *testing.T) {
	t.Parallel()
	// M3b treats inbound results as log-only (M3c adds matching).
	// This test just checks the code path does not crash and does
	// not send anything.
	p := newSilentProtocol()
	reply := &captureReply{}

	payload, _ := swarmsearch.EncodeResult(swarmsearch.Result{TxID: 1})
	p.HandleMessage("10.0.0.1:6881", payload, reply.fn())

	if _, ok := reply.last(); ok {
		t.Error("inbound result caused an outbound message in M3b")
	}
}
