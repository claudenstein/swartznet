package testlab_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/testlab"
)

// TestDHTClusterWireMeshConverges verifies that on a DHT-enabled
// cluster, the sn_search peer-wire mesh reliably forms once
// WireMesh is called — i.e. nothing in the DHT-on path
// interferes with direct peer-engine hints.
//
// Background: without WireMesh, the DHT cluster's sn_search mesh
// does NOT converge. Peer-wire discovery via DHT needs someone
// to announce_peer for the shared infohash, but nothing does
// (the testlab shared infohash has no real torrent behind it).
// get_peers returns only the closest-nodes list, which
// anacrolix does NOT interpret as peer-wire targets for this
// torrent. In the iteration that landed this test, a 4-node
// cluster produced 3/4 nodes with capable_peers ≥ 1 after 15 s
// and one isolated node — consistent with some nodes finding
// each other through incidental DHT traffic but no guaranteed
// discovery.
//
// Contract captured here: callers that need a sn_search mesh
// on top of the DHT MUST call WireMesh explicitly. This test
// is the load-bearing gate for that promise.
func TestDHTClusterWireMeshConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DHT+WireMesh convergence in -short mode")
	}

	const n = 4
	c := testlab.NewDHTCluster(t, n)

	// Explicit mesh hints — short-circuits DHT discovery for
	// peer-wire connectivity and gets every pair to attempt a
	// TCP handshake immediately.
	c.WireMesh(t)
	c.WaitAllHandshaked(t, 15*time.Second)

	// Post-condition: every node must see (n-1) sn_search-
	// capable peers.
	want := n - 1
	for i, node := range c.Nodes {
		p := node.Eng.SwarmSearch()
		if p == nil {
			t.Errorf("node %d: SwarmSearch() is nil", i)
			continue
		}
		capable := p.CapablePeerCount()
		if capable < want {
			t.Errorf("node %d: capable_peers=%d, want %d", i, capable, want)
		}
	}
}
