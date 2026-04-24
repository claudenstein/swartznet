package testlab_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioBitSetReconciliationAdvertised validates that two
// v0.5+ nodes exchange peer_announce frames carrying
// BitSetReconciliation (services bit 9 = 0x200). This is the
// gate the sync handler checks before dispatching msg_types 4-8;
// if nodes don't advertise the bit across the wire, every sync
// frame gets rejected with code 2, regardless of how correct
// their internal state is.
//
// Mirrors TestScenarioPeerAnnounceServices but for the
// Aggregate-track capability bit instead of the legacy bits.
func TestScenarioBitSetReconciliationAdvertised(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)

	// peer_announce is fire-and-forget from a goroutine — give
	// both sides a brief window to ingest.
	time.Sleep(300 * time.Millisecond)

	for i, n := range c.Nodes {
		var saw bool
		for _, ps := range n.Eng.SwarmSearch().KnownPeers() {
			if !ps.Supported {
				continue
			}
			if !ps.Services.Has(swarmsearch.BitSetReconciliation) {
				continue
			}
			saw = true
			t.Logf("node %d: peer %s advertises BitSetReconciliation (services=%016x)",
				i, ps.Addr, uint64(ps.Services))
		}
		if !saw {
			c.DumpLogs(t)
			t.Errorf("node %d: no peer advertised BitSetReconciliation", i)
		}
	}
}

// Belt-and-braces: DefaultServices itself must include the bit.
// Guards against accidental removal in future refactors since
// adding the bit to DefaultServices is what makes every v0.5
// node automatically advertise sync capability.
func TestDefaultServicesIncludesBitSetReconciliation(t *testing.T) {
	s := swarmsearch.DefaultServices()
	if !s.Has(swarmsearch.BitSetReconciliation) {
		t.Errorf("DefaultServices = %016x, missing BitSetReconciliation (0x%016x)",
			uint64(s), uint64(swarmsearch.BitSetReconciliation))
	}
}
