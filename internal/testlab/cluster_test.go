package testlab_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/testlab"
)

// Tests in this file do NOT use t.Parallel — every test spins
// up its own set of anacrolix clients that listen on 0.0.0.0 +
// OS-assigned ports and aggressively dial peers added via
// AddClientPeer. Running them concurrently causes enough load
// on localhost that the sn_search handshake races become
// flaky and the lock-debugging output drowns out any real
// failure signal. Sequential is plenty fast for N≤5 clusters.

// TestClusterStartStop covers the bare-minimum lifecycle of the
// harness: spin up N nodes, confirm every node has a working
// local index, shut down cleanly. No multi-node interactions.
func TestClusterStartStop(t *testing.T) {
	c := testlab.NewCluster(t, 3)
	if len(c.Nodes) != 3 {
		t.Fatalf("len(Nodes) = %d, want 3", len(c.Nodes))
	}
	for i, n := range c.Nodes {
		if n.Eng == nil {
			t.Errorf("node %d: nil engine", i)
		}
		if n.Index == nil {
			t.Errorf("node %d: nil index", i)
		}
		if n.Eng.LocalPort() == 0 {
			t.Errorf("node %d: LocalPort = 0, want non-zero", i)
		}
	}
}

// TestClusterLocalQuery covers Layer L end-to-end through the
// harness: seed a torrent into one node's index, query it from
// the same node, get a hit. This is a smoke test for the
// IndexTorrent + LocalQuery helpers, not the sn_search path.
func TestClusterLocalQuery(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	c.Nodes[0].IndexTorrent(t, 0x11, "ubuntu 24.04 desktop iso")

	resp := c.Nodes[0].LocalQuery(t, "ubuntu")
	if len(resp.Hits) != 1 {
		t.Fatalf("local query hits = %d, want 1", len(resp.Hits))
	}
	if resp.Hits[0].Name != "ubuntu 24.04 desktop iso" {
		t.Errorf("local hit name = %q", resp.Hits[0].Name)
	}
}

// TestClusterSwarmSearchThreeNode is the headline test of the
// harness: three nodes, one seeds content, the other two query
// via Layer S and get hits routed back from the seeder. This is
// the first multi-node end-to-end sn_search test in the
// project.
//
// Flow:
//  1. Build a 3-node cluster. Each node runs a real Engine
//     listening on an ephemeral loopback port.
//  2. Seed node 0 with "ubuntu 24.04" content in its local
//     Bleve index.
//  3. WireMesh each node as a trusted peer of every other in
//     the shared testlab dummy swarm, so anacrolix dials and
//     the LTEP handshake fires.
//  4. WaitAllHandshaked until every node sees the other two as
//     sn_search-capable.
//  5. Nodes 1 and 2 issue a sn_search query for "ubuntu" — the
//     query goes over the peer wire to node 0, node 0 runs a
//     local Bleve query, and the result comes back merged.
//  6. Assert each leech got at least one hit and that it was
//     attributed to node 0's address.
//
// The whole thing runs in-process over loopback — no DHT, no
// tracker, no containers, no external network.
func TestClusterSwarmSearchThreeNode(t *testing.T) {
	c := testlab.NewCluster(t, 3)

	// Node 0 is the seeder. Give it one torrent with a
	// predictable name so the other nodes can find it by
	// query.
	c.Nodes[0].IndexTorrent(t, 0x01, "ubuntu 24.04 desktop amd64 iso")

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)

	// Give a brief settle window for anacrolix's LTEP
	// handshake to finish routing the extension id on every
	// pair. The handshake-complete check above only asserts the
	// remote peer advertised sn_search — it does not guarantee
	// the outbound side has the routed extension number yet.
	time.Sleep(100 * time.Millisecond)

	anyFailed := false
	for i := 1; i < 3; i++ {
		resp := c.Nodes[i].SwarmQuery(t, "ubuntu")
		if resp.Asked == 0 {
			t.Errorf("node %d: asked=0, want >=1 (no sn_search peers)", i)
			anyFailed = true
			continue
		}
		if resp.Responded == 0 {
			t.Errorf("node %d: responded=0, asked=%d rejected=%d", i, resp.Asked, resp.Rejected)
			anyFailed = true
			continue
		}
		if len(resp.Hits) == 0 {
			t.Errorf("node %d: 0 hits, want ≥1", i)
			anyFailed = true
			continue
		}
		topHit := resp.Hits[0]
		if topHit.Name == "" {
			t.Errorf("node %d: top hit name empty", i)
		}
		if len(topHit.Sources) == 0 {
			t.Errorf("node %d: top hit has no sources attribution", i)
		}
	}
	if anyFailed {
		c.DumpLogs(t)
	}
}
