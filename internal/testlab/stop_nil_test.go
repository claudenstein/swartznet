package testlab_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/testlab"
)

// TestClusterStopSkipsNilNodes covers the `n == nil` continue
// branch in Cluster.Stop. We set Nodes[0] to nil before the
// auto-cleanup fires, simulating a partial failure where the
// outer test code zeroed an entry. Stop must not nil-deref.
func TestClusterStopSkipsNilNodes(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	if len(c.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(c.Nodes))
	}
	c.Nodes[0] = nil
	// t.Cleanup will invoke Stop again at test exit; both calls
	// must skip the nil node without panicking.
	c.Stop()
}

// TestClusterSharedInfoHashIsNonZero pins the SharedInfoHash
// getter; it returns a deterministic non-zero array.
func TestClusterSharedInfoHashIsNonZero(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	got := c.SharedInfoHash()
	var zero [20]byte
	if got == zero {
		t.Error("SharedInfoHash should be non-zero")
	}
}
