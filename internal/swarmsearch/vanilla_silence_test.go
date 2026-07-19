package swarmsearch

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// recordTransport records every SendExtension by target address. It models the
// engine transport WITHOUT sockets, so the vanilla-silence guarantee runs
// deterministically in CI (the real-loopback version stays in wirecompat/
// scenarios). SendExtension can only be reached with a valid PeerToken, and a
// token is minted ONLY from an sn_search m-dict advertisement — so a vanilla
// peer (which never advertised) is structurally unreachable.
type recordTransport struct {
	mu    sync.Mutex
	sends map[string]int
}

func newRecordTransport() *recordTransport { return &recordTransport{sends: map[string]int{}} }

func (r *recordTransport) SendExtension(peer PeerToken, _ []byte) error {
	r.mu.Lock()
	r.sends[peer.Addr()]++
	r.mu.Unlock()
	return nil
}

func (r *recordTransport) count(addr string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sends[addr]
}

// TestVanillaPeerNeverReceivesFrames is the deterministic mainline-compat gate:
// a peer that did NOT advertise sn_search in its LTEP handshake receives ZERO
// sn_search frames — no peer_announce, no query — while a peer that did
// advertise gets both.
func TestVanillaPeerNeverReceivesFrames(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer p.Close()
	rec := newRecordTransport()
	p.SetTransport(rec)
	p.SetCapabilitySource(fullShare())
	var pk [32]byte
	pk[0] = 0xAB
	p.SetPublisherPubkey(pk, true)

	const vanilla = "vanilla:6881"
	const capable = "capable:6881"

	// A vanilla peer: added, handshake advertises NO sn_search (id 0).
	p.NotePeerAdded(vanilla)
	p.OnRemoteHandshake(vanilla, false, 0)
	// A capable peer: advertises sn_search → token minted → peer_announce enqueued.
	p.NotePeerAdded(capable)
	p.OnRemoteHandshake(capable, true, 7)

	// (1) The outbound peer_announce is async; wait for the capable peer to get it.
	deadline := time.Now().Add(2 * time.Second)
	for rec.count(capable) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if rec.count(capable) == 0 {
		t.Fatal("capable peer never received its peer_announce")
	}
	if n := rec.count(vanilla); n != 0 {
		t.Fatalf("vanilla peer received %d peer_announce frames, want 0", n)
	}

	// (2) A query fans out only to token-holding (capable) peers. Give it a
	// short deadline — the recording transport never replies, and the send
	// (what we assert) happens before Query waits for responses.
	beforeCap := rec.count(capable)
	qctx, qcancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer qcancel()
	if _, err := p.Query(qctx, QueryRequest{Q: "ubuntu", Scope: "n", Limit: 10}); err != nil {
		t.Fatalf("query: %v", err)
	}
	if rec.count(capable) <= beforeCap {
		t.Error("capable peer did not receive the query frame")
	}
	if n := rec.count(vanilla); n != 0 {
		t.Fatalf("vanilla peer received %d frames total, want 0 (mainline-compat break)", n)
	}
	// Sanity: exactly one capable peer is counted as search-capable.
	if p.CapablePeerCount() != 1 {
		t.Errorf("capable peer count = %d, want 1", p.CapablePeerCount())
	}
}

// TestBannedPeerNeverAdvertisedIsSilent: a banned peer never gets a token, so it
// is silent even if it later advertises.
func TestBannedPeerNeverMintsToken(t *testing.T) {
	t.Parallel()
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer p.Close()
	rec := newRecordTransport()
	p.SetTransport(rec)
	p.SetCapabilitySource(fullShare())

	const bad = "banned:6881"
	// Ban the peer hard, then let it advertise.
	for i := 0; i < 200; i++ {
		p.ban.Add(bad, 1)
	}
	p.NotePeerAdded(bad)
	p.OnRemoteHandshake(bad, true, 3) // advertises, but is banned → no token
	time.Sleep(100 * time.Millisecond)
	if n := rec.count(bad); n != 0 {
		t.Fatalf("banned peer received %d frames, want 0", n)
	}
}
