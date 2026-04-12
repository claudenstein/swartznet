package testlab_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioPeerAnnounceServices validates M15b-wire: after
// the LTEP handshake, each node sends a PeerAnnounce carrying
// its ServiceBits to the remote peer. The remote's KnownPeers
// should then reflect those services.
//
// This is the first test of the additive-feature-bits wire
// format inspired by Bitcoin Core's peer services field.
func TestScenarioPeerAnnounceServices(t *testing.T) {
	c := testlab.NewCluster(t, 2)

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)

	// PeerAnnounce is fire-and-forget from a goroutine, so
	// give a brief settle window for both sides to process
	// the announce.
	time.Sleep(300 * time.Millisecond)

	for i, n := range c.Nodes {
		peers := n.Eng.SwarmSearch().KnownPeers()
		var announced int
		for _, ps := range peers {
			if ps.Supported && ps.Services != 0 {
				announced++
				// The remote should have announced at least
				// BitShareLocal | BitFileHits | BitContentHits
				// (the DefaultServices mask).
				if !ps.Services.Has(swarmsearch.BitShareLocal) {
					t.Errorf("node %d: peer %s missing BitShareLocal (services=%064b)",
						i, ps.Addr, ps.Services)
				}
				if !ps.Services.Has(swarmsearch.BitFileHits) {
					t.Errorf("node %d: peer %s missing BitFileHits",
						i, ps.Addr)
				}
				if !ps.Services.Has(swarmsearch.BitContentHits) {
					t.Errorf("node %d: peer %s missing BitContentHits",
						i, ps.Addr)
				}
				if ps.Version != swarmsearch.ProtocolVersion {
					t.Errorf("node %d: peer %s version=%d, want %d",
						i, ps.Addr, ps.Version, swarmsearch.ProtocolVersion)
				}
			}
		}
		if announced == 0 {
			c.DumpLogs(t)
			t.Errorf("node %d: no peer announced services; total known=%d",
				i, len(peers))
		} else {
			t.Logf("node %d: %d peers announced services", i, announced)
		}
	}
}
