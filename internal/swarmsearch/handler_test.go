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
