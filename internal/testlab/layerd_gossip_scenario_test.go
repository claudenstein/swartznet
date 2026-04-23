package testlab_test

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestLayerDGossipEndToEnd is the in-process counterpart to
// testbed scenario s12, one level stricter than
// TestLayerDDHTClusterRoundTrip. It exercises the complete
// Layer-S → gossip → Layer-D pipeline that the docker scenario
// depends on:
//
//  1. DHT cluster on loopback — seeds + leech all run the real
//     anacrolix DHT via engine.Engine.
//  2. WireMesh — every pair of nodes has a peer-wire connection
//     so the sn_search LTEP handshake converges.
//  3. Seeds run the real *dhtindex.Publisher (started by
//     Engine.startPublisher, backed by AnacrolixPutter writing
//     into the shared DHT).
//  4. Caps.Publisher=1 is set automatically on seeds by
//     engine.startPublisher so their outbound PeerAnnounce
//     frames carry the `pk` gossip field.
//  5. sn_search gossip: on handshake, each seed announces its
//     publisher pubkey to the leech, which feeds it into the
//     leech's engine.Lookup via the gossipIndexerSink. NO
//     manual AddIndexer call.
//  6. Seeds submit a keyword through their publishers, which
//     drives getput.Put against each other's DHT servers.
//  7. Leech issues a Lookup.Query for the keyword. Assertions:
//     every seed pubkey was asked (proof the gossip path
//     cross-registered them), every seed responded, and the
//     merged hit list contains each seed's distinct infohash.
//
// This is what s12 tries to assert in docker. The only
// difference is the network layer; if s12 fails while this
// passes, the failure is strictly containerised-networking and
// the engine-level code is fine.
func TestLayerDGossipEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gossip end-to-end in -short mode")
	}

	const (
		nSeeds   = 2
		nLeeches = 1
		total    = nSeeds + nLeeches
		keyword  = "gossipcorpus"
	)

	c := testlab.NewDHTCluster(t, total)

	// Wire the sn_search mesh up front so the seeds' capability
	// bits (which engine.startPublisher already set to
	// Publisher=1) propagate to the leech via the PeerAnnounce
	// handshake, and the leech's gossipIndexerSink populates its
	// Lookup's known-indexer set.
	c.WireMesh(t)
	c.WaitAllHandshaked(t, 15*time.Second)

	// Give DHT a beat to finish settling.
	time.Sleep(1500 * time.Millisecond)

	// Distinct fixture infohashes per seed so we can tell the
	// merged hit set apart by origin.
	var fixtures [nSeeds][20]byte
	for s := 0; s < nSeeds; s++ {
		for i := range fixtures[s] {
			fixtures[s][i] = byte(0xC0 + s)
		}
		pub := c.Nodes[s].Eng.Publisher()
		if pub == nil {
			t.Fatalf("seed %d: Engine.Publisher() is nil", s)
		}
		pub.Submit(dhtindex.PublishTask{
			InfoHash: fixtures[s][:],
			Name:     keyword,
			Seeders:  1,
		})
	}
	if err := waitPublished(c, keyword, 20*time.Second); err != nil {
		c.DumpLogs(t)
		t.Fatalf("seeds did not emit a BEP-44 put for %q: %v", keyword, err)
	}

	// The leech's Lookup SHOULD already know both seed pubkeys
	// via sn_search gossip. We verify that directly before
	// issuing the DHT query — if the gossip path regressed,
	// the Lookup has only "self" and we'd silently fall back
	// to a one-node query.
	leech := c.Nodes[nSeeds]
	look := leech.Eng.Lookup()
	if look == nil {
		t.Fatalf("leech: Engine.Lookup() is nil")
	}

	// Indexers include "self" plus each gossiped peer. Self is
	// always registered; we subtract it from the expected count.
	wantSeedPubkeys := make(map[[32]byte]bool, nSeeds)
	for s := 0; s < nSeeds; s++ {
		wantSeedPubkeys[c.Nodes[s].Eng.Identity().PublicKeyBytes()] = true
	}
	meetDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(meetDeadline) {
		haveSeedPubkeys := 0
		for _, info := range look.Indexers() {
			if wantSeedPubkeys[info.PubKey] {
				haveSeedPubkeys++
			}
		}
		if haveSeedPubkeys == nSeeds {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	missing := 0
	for pk := range wantSeedPubkeys {
		found := false
		for _, info := range look.Indexers() {
			if info.PubKey == pk {
				found = true
				break
			}
		}
		if !found {
			missing++
			t.Errorf("gossip did NOT register seed pubkey %s as indexer",
				hex.EncodeToString(pk[:8]))
		}
	}
	if missing > 0 {
		c.DumpLogs(t)
		t.FailNow()
	}

	// Now the real Layer-D lookup. Assertions identical to
	// TestLayerDDHTClusterRoundTrip but expressed strictly on
	// gossiped indexers (no manual AddIndexer).
	deadline := time.Now().Add(20 * time.Second)
	var lastResp *dhtindex.LookupResponse
	for time.Now().Before(deadline) {
		qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, err := look.Query(qctx, keyword)
		cancel()
		if err == nil {
			lastResp = resp
			if resp.IndexersResponded >= nSeeds && len(resp.Hits) >= nSeeds {
				// Both fixtures present?
				seen := make(map[string]bool)
				for _, h := range resp.Hits {
					seen[h.InfoHash] = true
				}
				allFixturesSeen := true
				for s := 0; s < nSeeds; s++ {
					ih := hex.EncodeToString(fixtures[s][:])
					if !seen[ih] {
						allFixturesSeen = false
						break
					}
				}
				if allFixturesSeen {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastResp == nil {
		t.Fatalf("Layer-D lookup never succeeded")
	}
	c.DumpLogs(t)
	t.Fatalf("gossip-driven Layer-D lookup did not converge: "+
		"asked=%d responded=%d hits=%d (want responded>=%d hits>=%d)",
		lastResp.IndexersAsked, lastResp.IndexersResponded, len(lastResp.Hits),
		nSeeds, nSeeds)
}
