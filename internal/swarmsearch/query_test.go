package swarmsearch_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	pp "github.com/anacrolix/torrent/peer_protocol"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// fanoutSender is a Sender test double that records every outbound
// (peer, payload) and can optionally inject a scripted response back
// into the Protocol on behalf of a peer — simulating an inbound
// sn_search Result or Reject arriving over the peer wire.
type fanoutSender struct {
	protocol *swarmsearch.Protocol

	mu    sync.Mutex
	sent  []fanoutMsg
	after map[string]func(p *swarmsearch.Protocol, peer string, q swarmsearch.Query)
}

type fanoutMsg struct {
	peer    string
	payload []byte
}

func newFanoutSender() *fanoutSender {
	return &fanoutSender{
		after: make(map[string]func(p *swarmsearch.Protocol, peer string, q swarmsearch.Query)),
	}
}

// setAfter sets the scripted response for a peer. Must be used
// instead of direct map writes because the PeerAnnounce
// goroutine from OnRemoteHandshake races with test setup.
func (f *fanoutSender) setAfter(peer string, fn func(*swarmsearch.Protocol, string, swarmsearch.Query)) {
	f.mu.Lock()
	f.after[peer] = fn
	f.mu.Unlock()
}

func (f *fanoutSender) Send(peer string, payload []byte) error {
	f.mu.Lock()
	f.sent = append(f.sent, fanoutMsg{peer: peer, payload: bytes.Clone(payload)})
	after := f.after[peer]
	f.mu.Unlock()

	if after != nil {
		q, err := swarmsearch.DecodeQuery(payload)
		if err == nil {
			// Run the scripted response on a separate goroutine so
			// Query's collector has a chance to start reading from
			// its pending-results channel.
			go after(f.protocol, peer, q)
		}
	}
	return nil
}

// newProtocolWithSender constructs a Protocol, attaches the sender,
// and wires the back-reference so scripted responses can call
// HandleMessage.
func newProtocolWithSender(t *testing.T, s *fanoutSender) *swarmsearch.Protocol {
	t.Helper()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetSender(s)
	s.protocol = p
	return p
}

// markCapable simulates a peer handshake so the query fan-out
// considers the peer eligible.
func markCapable(p *swarmsearch.Protocol, addr string) {
	p.NotePeerAdded(addr)
	p.OnRemoteHandshake(addr, &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 11,
		},
	})
}

// scriptedResult builds an after-callback that replies with a fixed
// Result list from the given peer.
func scriptedResult(hits []swarmsearch.Hit) func(*swarmsearch.Protocol, string, swarmsearch.Query) {
	return func(p *swarmsearch.Protocol, peer string, q swarmsearch.Query) {
		payload, _ := swarmsearch.EncodeResult(swarmsearch.Result{
			TxID:  q.TxID,
			Total: len(hits),
			Hits:  hits,
		})
		p.HandleMessage(peer, payload, nil)
	}
}

// scriptedReject builds an after-callback that replies with a
// Reject message.
func scriptedReject(code int, reason string) func(*swarmsearch.Protocol, string, swarmsearch.Query) {
	return func(p *swarmsearch.Protocol, peer string, q swarmsearch.Query) {
		payload, _ := swarmsearch.EncodeReject(swarmsearch.Reject{
			TxID:   q.TxID,
			Code:   code,
			Reason: reason,
		})
		p.HandleMessage(peer, payload, nil)
	}
}

func ih(seed byte) []byte { return bytes.Repeat([]byte{seed}, 20) }

func TestQueryNoCapablePeers(t *testing.T) {
	t.Parallel()
	s := newFanoutSender()
	p := newProtocolWithSender(t, s)

	// Track a peer, but never call OnRemoteHandshake — it is not
	// marked capable.
	p.NotePeerAdded("1.2.3.4:6881")

	_, err := p.Query(context.Background(), swarmsearch.QueryRequest{Q: "ubuntu"})
	if err != swarmsearch.ErrNoCapablePeers {
		t.Errorf("err = %v, want ErrNoCapablePeers", err)
	}
}

func TestQueryEmptyRejected(t *testing.T) {
	t.Parallel()
	s := newFanoutSender()
	p := newProtocolWithSender(t, s)
	markCapable(p, "1.2.3.4:6881")

	_, err := p.Query(context.Background(), swarmsearch.QueryRequest{Q: ""})
	if err != swarmsearch.ErrEmptyQuery {
		t.Errorf("err = %v, want ErrEmptyQuery", err)
	}
}

func TestQueryFanoutAndMerge(t *testing.T) {
	t.Parallel()
	s := newFanoutSender()
	p := newProtocolWithSender(t, s)

	// Three capable peers, each returning a different hit. Peer A and
	// peer B both return the SAME infohash (IH=0xaa), so the merge
	// logic should collapse them into one MergedHit with two Sources.
	markCapable(p, "10.0.0.1:6881")
	markCapable(p, "10.0.0.2:6881")
	markCapable(p, "10.0.0.3:6881")

	s.setAfter("10.0.0.1:6881", scriptedResult([]swarmsearch.Hit{
		{IH: ih(0xaa), N: "Ubuntu 24.04", Sz: 6 * 1024 * 1024 * 1024, S: 100, Rank: 500},
	}))
	s.setAfter("10.0.0.2:6881", scriptedResult([]swarmsearch.Hit{
		{IH: ih(0xaa), N: "", Sz: 0, S: 130, Rank: 400}, // higher seeders, empty name
	}))
	s.setAfter("10.0.0.3:6881", scriptedResult([]swarmsearch.Hit{
		{IH: ih(0xbb), N: "Debian Bookworm", Sz: 1 * 1024 * 1024 * 1024, S: 10, Rank: 300},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := p.Query(ctx, swarmsearch.QueryRequest{Q: "linux"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.Asked != 3 {
		t.Errorf("Asked = %d, want 3", resp.Asked)
	}
	if resp.Responded != 3 {
		t.Errorf("Responded = %d, want 3", resp.Responded)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("len(Hits) = %d, want 2 (dedup by infohash)", len(resp.Hits))
	}
	// Hit 1 should be the 0xaa merge: score summed (500+400=900,
	// capped at 1000), name from peer 1, size from peer 1, max
	// seeders = 130. Two sources.
	var aa *swarmsearch.MergedHit
	for i := range resp.Hits {
		if resp.Hits[i].InfoHash[0] == 'a' {
			aa = &resp.Hits[i]
			break
		}
	}
	if aa == nil {
		t.Fatal("no merged hit for 0xaa")
	}
	if aa.Name != "Ubuntu 24.04" {
		t.Errorf("merged name = %q, want 'Ubuntu 24.04'", aa.Name)
	}
	if aa.Score != 900 {
		t.Errorf("merged score = %d, want 900", aa.Score)
	}
	if aa.Seeders != 130 {
		t.Errorf("merged seeders = %d, want 130", aa.Seeders)
	}
	if len(aa.Sources) != 2 {
		t.Errorf("sources = %v, want 2 entries", aa.Sources)
	}
}

func TestQueryPartialReject(t *testing.T) {
	t.Parallel()
	s := newFanoutSender()
	p := newProtocolWithSender(t, s)
	markCapable(p, "10.0.0.1:6881")
	markCapable(p, "10.0.0.2:6881")

	s.setAfter("10.0.0.1:6881", scriptedResult([]swarmsearch.Hit{
		{IH: ih(0x11), N: "Found", Rank: 700},
	}))
	s.setAfter("10.0.0.2:6881", scriptedReject(
		swarmsearch.RejectRateLimited, "over quota"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := p.Query(ctx, swarmsearch.QueryRequest{Q: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Asked != 2 {
		t.Errorf("Asked = %d, want 2", resp.Asked)
	}
	if resp.Responded != 1 {
		t.Errorf("Responded = %d, want 1", resp.Responded)
	}
	if resp.Rejected != 1 {
		t.Errorf("Rejected = %d, want 1", resp.Rejected)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].Name != "Found" {
		t.Errorf("hits = %+v, want one 'Found' hit", resp.Hits)
	}
}

func TestQueryTimeoutWhenNoResponse(t *testing.T) {
	t.Parallel()
	s := newFanoutSender()
	p := newProtocolWithSender(t, s)
	markCapable(p, "10.0.0.1:6881")
	// No after-callback set — the peer never replies.

	start := time.Now()
	resp, err := p.Query(context.Background(), swarmsearch.QueryRequest{
		Q:       "never",
		Timeout: 80 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Query waited %v, expected ~80ms", elapsed)
	}
	if resp.Responded != 0 {
		t.Errorf("Responded = %d, want 0", resp.Responded)
	}
	if len(resp.Hits) != 0 {
		t.Errorf("hits = %v, want empty on timeout", resp.Hits)
	}
}
