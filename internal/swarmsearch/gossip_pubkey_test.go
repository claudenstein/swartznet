package swarmsearch_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	pp "github.com/anacrolix/torrent/peer_protocol"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// recordingSink captures NoteGossipIndexer calls for assertion.
type recordingSink struct {
	mu      sync.Mutex
	entries []recordedEntry
}

type recordedEntry struct {
	pubkey [32]byte
	label  string
}

func (r *recordingSink) NoteGossipIndexer(pub [32]byte, label string) {
	r.mu.Lock()
	r.entries = append(r.entries, recordedEntry{pubkey: pub, label: label})
	r.mu.Unlock()
}

func (r *recordingSink) snapshot() []recordedEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// TestPeerAnnouncePubkeyFlowsToSink closes wire-compat matrix row
// 8.4-C: a node that runs the Layer-D publisher gossips its
// pubkey in the `pk` field of PeerAnnounce, and the receiver
// registers it via the attached IndexerSink. Without this path
// two publisher nodes would never learn about each other's
// indexes through peer-wire handshakes alone, breaking the
// spec's promise that "both add each other's pubkeys to their
// gossip-discovered indexer set after first lt_search handshake."
//
// The test simulates exactly that: register a peer via the
// handshake callback, deliver a PeerAnnounce with a 32-byte pk,
// and assert (a) PeerState carries the pubkey, (b) the sink
// received the pubkey with a label derived from the peer addr.
func TestPeerAnnouncePubkeyFlowsToSink(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink := &recordingSink{}
	p.SetIndexerSink(sink)

	const peer = "198.51.100.4:6881"
	// Pre-register the peer so the handler's PeerState update
	// path runs (without a registered peer the pk is still sent
	// to the sink but not stashed on PeerState — the explicit
	// handshake makes the contract clearer).
	p.OnRemoteHandshake(peer, &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 42,
		},
	})

	var pub [32]byte
	for i := range pub {
		pub[i] = byte(0xA0 + i)
	}
	body, err := swarmsearch.EncodePeerAnnounce(swarmsearch.PeerAnnounce{
		Version:  swarmsearch.ProtocolVersion,
		Services: uint64(swarmsearch.BitShareLocal | swarmsearch.BitLayerDPublisher),
		Pubkey:   pub[:],
	})
	if err != nil {
		t.Fatalf("EncodePeerAnnounce: %v", err)
	}
	p.HandleMessage(peer, body, nil)

	entries := sink.snapshot()
	if len(entries) != 1 {
		t.Fatalf("sink got %d entries, want 1 (entries=%+v)", len(entries), entries)
	}
	if entries[0].pubkey != pub {
		t.Errorf("sink pubkey = %x, want %x", entries[0].pubkey, pub)
	}
	// Label must contain the remote address so operators can
	// trace the pubkey back to the source connection.
	if want := "gossip:" + peer; entries[0].label != want {
		t.Errorf("sink label = %q, want %q", entries[0].label, want)
	}

	// PeerState inspection: the peer's PublisherPubkey must be
	// set to what we gossiped.
	found := false
	for _, ps := range p.KnownPeers() {
		if ps.Addr != peer {
			continue
		}
		if ps.PublisherPubkey != pub {
			t.Errorf("PeerState.PublisherPubkey = %x, want %x", ps.PublisherPubkey, pub)
		}
		found = true
	}
	if !found {
		t.Errorf("no PeerState for %s", peer)
	}

	// Misbehavior must NOT be charged on a clean pk-carrying
	// announce — this line is the "no false positive" canary.
	if s := p.MisbehaviorScore(peer); s != 0 {
		t.Errorf("MisbehaviorScore = %d, want 0 after clean pk announce", s)
	}
}

// TestPeerAnnouncePubkeyWrongLengthIgnored ensures the sink is
// NOT called when the peer gossips a pk field that isn't exactly
// 32 bytes. Anything else is either truncation, corruption, or a
// malicious peer trying to spam our indexer set with garbage —
// we silently drop it (the announce itself is still processed
// for Services/Version).
func TestPeerAnnouncePubkeyWrongLengthIgnored(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink := &recordingSink{}
	p.SetIndexerSink(sink)

	const peer = "198.51.100.5:6881"
	p.OnRemoteHandshake(peer, &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 7,
		},
	})

	// 31 bytes (off by one) should be rejected.
	shortKey := make([]byte, 31)
	body, err := swarmsearch.EncodePeerAnnounce(swarmsearch.PeerAnnounce{
		Version:  swarmsearch.ProtocolVersion,
		Services: uint64(swarmsearch.BitShareLocal),
		Pubkey:   shortKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage(peer, body, nil)

	if entries := sink.snapshot(); len(entries) != 0 {
		t.Errorf("sink got %d entries for 31-byte pk, want 0", len(entries))
	}
}

// TestPeerAnnounceNoPubkeyNoSinkCall verifies the backwards-
// compat path: an older peer that doesn't set `pk` triggers no
// sink call at all. Without this the old handshake path would
// accidentally register a zero-pubkey as a gossip indexer, which
// would then fan out queries to a key nobody owns.
func TestPeerAnnounceNoPubkeyNoSinkCall(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink := &recordingSink{}
	p.SetIndexerSink(sink)

	const peer = "198.51.100.6:6881"
	p.OnRemoteHandshake(peer, &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 9,
		},
	})

	body, err := swarmsearch.EncodePeerAnnounce(swarmsearch.PeerAnnounce{
		Version:  swarmsearch.ProtocolVersion,
		Services: uint64(swarmsearch.BitShareLocal),
		// Pubkey intentionally nil.
	})
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage(peer, body, nil)

	if entries := sink.snapshot(); len(entries) != 0 {
		t.Errorf("sink got %d entries for missing pk, want 0", len(entries))
	}
}

// TestPeerAnnounceZeroPubkeyRejected ensures the all-zero pubkey is
// silently dropped at the gossip-sink boundary. A 32-byte zero
// pubkey cannot correspond to a real ed25519 identity, but the
// type system can't reject it — letting it through would let a
// misbehaving peer quietly grow every receiver's indexer fan-out
// set by a useless entry on each reconnect. PeerState must stay
// clean too (no stashed zero-key).
func TestPeerAnnounceZeroPubkeyRejected(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	sink := &recordingSink{}
	p.SetIndexerSink(sink)

	const peer = "198.51.100.7:6881"
	p.OnRemoteHandshake(peer, &pp.ExtendedHandshakeMessage{
		M: map[pp.ExtensionName]pp.ExtensionNumber{
			swarmsearch.ExtensionName: 11,
		},
	})

	zero := make([]byte, 32) // all-zero 32-byte key
	body, err := swarmsearch.EncodePeerAnnounce(swarmsearch.PeerAnnounce{
		Version:  swarmsearch.ProtocolVersion,
		Services: uint64(swarmsearch.BitShareLocal),
		Pubkey:   zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.HandleMessage(peer, body, nil)

	if entries := sink.snapshot(); len(entries) != 0 {
		t.Errorf("sink got %d entries for zero-pk, want 0", len(entries))
	}
	// PeerState must still reflect the Services/Version from the
	// rest of the announce, but the PublisherPubkey must remain
	// the zero value (i.e. never explicitly stored).
	for _, ps := range p.KnownPeers() {
		if ps.Addr != peer {
			continue
		}
		if ps.PublisherPubkey != ([32]byte{}) {
			t.Errorf("PublisherPubkey = %x, want all-zero (not stashed)", ps.PublisherPubkey)
		}
		if ps.Services != swarmsearch.BitShareLocal {
			t.Errorf("Services = %v, want %v (non-pk fields still processed)",
				ps.Services, swarmsearch.BitShareLocal)
		}
	}
}
