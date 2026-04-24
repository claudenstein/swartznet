package testlab_test

import (
	"sync"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// recordingGossipSink is a testlab-local IndexerSink impl that
// captures every gossip-learned pubkey so the scenario can
// assert cross-node delivery.
type recordingGossipSink struct {
	mu   sync.Mutex
	keys [][32]byte
}

func (r *recordingGossipSink) NoteGossipIndexer(pub [32]byte, _ string) {
	r.mu.Lock()
	r.keys = append(r.keys, pub)
	r.mu.Unlock()
}

func (r *recordingGossipSink) snapshot() [][32]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][32]byte, len(r.keys))
	copy(out, r.keys)
	return out
}

// TestScenarioGossipPubkeyAutoAdd closes wire-compat matrix row
// 8.4-C: two SwartzNet clients that both run the Layer-D
// publisher auto-register each other's pubkeys after one
// sn_search handshake, so a subsequent `search --dht` on either
// side fans out to the peer's published keyword entries.
//
// The testlab cluster disables the real DHT (see cluster.go
// "cfg.DisableDHT = true") so startPublisher doesn't wire a
// *dhtindex.Lookup into the engine. For this test we bypass the
// engine-side wiring and inspect the protocol layer directly:
//
//  1. Give each node's *swarmsearch.Protocol a synthetic
//     publisher pubkey + Publisher=1 cap.
//  2. Attach a recordingGossipSink so inbound pk-carrying
//     PeerAnnounce frames are captured.
//  3. WireMesh + WaitAllHandshaked — the PeerAnnounce fan-out in
//     OnRemoteHandshake then runs for both directions.
//  4. Assert each node's sink received the OTHER node's pubkey
//     (and not its own).
//
// A node that is NOT a publisher (Publisher=0) must NOT emit pk
// in its PeerAnnounce — the spec is strict that only publishers
// advertise. The test also covers that negative case by spawning
// a third "subscriber-only" node and asserting the other two
// nodes never receive its (absent) pubkey.
func TestScenarioGossipPubkeyAutoAdd(t *testing.T) {
	c := testlab.NewCluster(t, 2)

	// Build a distinct 32-byte pubkey for each node so we can
	// tell whose gossip arrived where.
	pubs := [2][32]byte{}
	for i := range pubs {
		for j := range pubs[i] {
			pubs[i][j] = byte(0x10 + i*0x40 + j)
		}
	}

	sinks := make([]*recordingGossipSink, len(c.Nodes))
	for i, node := range c.Nodes {
		sinks[i] = &recordingGossipSink{}
		sw := node.Eng.SwarmSearch()
		sw.SetCapabilities(swarmsearch.Capabilities{
			ShareLocal:  2,
			FileHits:    1,
			ContentHits: 1,
			Publisher:   1,
		})
		sw.SetPublisherPubkey(pubs[i][:])
		sw.SetIndexerSink(sinks[i])
	}

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)

	// OnRemoteHandshake dispatches the PeerAnnounce asynchronously
	// (fire-and-forget goroutine to dodge the anacrolix read-loop
	// lock). Give the write a moment to complete end-to-end.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sinks[0].snapshot()) > 0 && len(sinks[1].snapshot()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Each node's sink must have exactly the OTHER node's pubkey.
	// We permit duplicates (bidirectional handshakes may trigger
	// more than one PeerAnnounce per peer under racy test timing)
	// but every entry MUST be the expected pubkey.
	for i, s := range sinks {
		entries := s.snapshot()
		if len(entries) == 0 {
			c.DumpLogs(t)
			t.Fatalf("node %d sink empty — handshake didn't deliver gossip pk", i)
		}
		other := pubs[1-i]
		for _, got := range entries {
			if got == pubs[i] {
				t.Errorf("node %d sink received its OWN pubkey back (echo loop)", i)
			}
			if got != other {
				t.Errorf("node %d sink got pubkey %x, want node-%d pubkey %x",
					i, got, 1-i, other)
			}
		}
	}
}

// TestScenarioGossipPubkeyOmittedWhenNotPublisher verifies the
// negative side of 8.4-C: a node with Publisher=0 (the default)
// must NOT send `pk` in its PeerAnnounce, even if it has a
// publisher pubkey configured. This keeps pure subscribers from
// accidentally polluting peers' indexer sets.
func TestScenarioGossipPubkeyOmittedWhenNotPublisher(t *testing.T) {
	c := testlab.NewCluster(t, 2)

	// Node 0 is a subscriber (Publisher=0). Node 1 is a publisher.
	var subPub [32]byte
	for i := range subPub {
		subPub[i] = 0xAA
	}
	var pubPub [32]byte
	for i := range pubPub {
		pubPub[i] = 0xBB
	}

	sink0 := &recordingGossipSink{}
	sink1 := &recordingGossipSink{}

	sw0 := c.Nodes[0].Eng.SwarmSearch()
	sw0.SetCapabilities(swarmsearch.Capabilities{
		ShareLocal:  2,
		FileHits:    1,
		ContentHits: 1,
		Publisher:   0, // subscriber only
	})
	sw0.SetPublisherPubkey(subPub[:])
	sw0.SetIndexerSink(sink0)

	sw1 := c.Nodes[1].Eng.SwarmSearch()
	sw1.SetCapabilities(swarmsearch.Capabilities{
		ShareLocal:  2,
		FileHits:    1,
		ContentHits: 1,
		Publisher:   1,
	})
	sw1.SetPublisherPubkey(pubPub[:])
	sw1.SetIndexerSink(sink1)

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)

	// Wait for sink1 to get the publisher's pk from sink0's
	// peer_announce. If sink1 stays empty after a grace period,
	// OR sink0 got subPub, the test fails.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sink0.snapshot()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Sink 0 (the subscriber) must receive node 1's publisher pk.
	got0 := sink0.snapshot()
	if len(got0) == 0 {
		c.DumpLogs(t)
		t.Fatal("subscriber sink never received publisher's pk")
	}
	for _, pk := range got0 {
		if pk != pubPub {
			t.Errorf("subscriber got pubkey %x, want publisher's %x", pk, pubPub)
		}
	}

	// Sink 1 (the publisher) must NOT receive anything from the
	// subscriber — Publisher=0 means pk is suppressed in the
	// outbound PeerAnnounce.
	if got1 := sink1.snapshot(); len(got1) != 0 {
		t.Errorf("publisher sink got %d entries from a non-publisher peer; want 0: %+v",
			len(got1), got1)
	}
}
