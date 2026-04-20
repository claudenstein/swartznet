package swarmsearch_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestPeerBookRecordFailureOnTriedPeer covers the tried-table
// branch of RecordFailure. Promote a peer to tried first, then
// RecordFailure should bump the Failures counter on it.
func TestPeerBookRecordFailureOnTriedPeer(t *testing.T) {
	t.Parallel()
	pb := swarmsearch.NewPeerBook(8, 8)
	pb.AddNew("203.0.113.1:6881")
	pb.Promote("203.0.113.1:6881") // moves to tried

	pb.RecordFailure("203.0.113.1:6881")
	// We can't easily inspect Failures, but the branch firing
	// without panicking is the contract; verify the peer is
	// still in tried (RecordFailure doesn't demote in v1).
	if !pb.IsTried("203.0.113.1:6881") {
		t.Error("RecordFailure on tried peer should not demote")
	}
}

// TestPeerBookRecordFailureOnNewPeer covers the newPeers-table
// branch of RecordFailure. AddNew without Promote leaves the
// peer in the new table.
func TestPeerBookRecordFailureOnNewPeer(t *testing.T) {
	t.Parallel()
	pb := swarmsearch.NewPeerBook(8, 8)
	pb.AddNew("203.0.113.2:6881")

	// Must not panic and must not crash trying to update Failures
	// on a new-table entry.
	pb.RecordFailure("203.0.113.2:6881")

	// The peer stays in NewAddrs (RecordFailure doesn't move it).
	addrs := pb.NewAddrs()
	found := false
	for _, a := range addrs {
		if a == "203.0.113.2:6881" {
			found = true
		}
	}
	if !found {
		t.Error("new peer dropped from NewAddrs after RecordFailure")
	}
}

// TestPeerBookPromoteEvictsAtTriedCap covers the LRQ-eviction
// branch in Promote. With maxTried=2, promote 3 distinct peers
// and the first one should be evicted.
func TestPeerBookPromoteEvictsAtTriedCap(t *testing.T) {
	t.Parallel()
	pb := swarmsearch.NewPeerBook(2, 8)

	for _, addr := range []string{"203.0.113.1:6881", "203.0.113.2:6881", "203.0.113.3:6881"} {
		pb.AddNew(addr)
		pb.Promote(addr)
	}

	if got := len(pb.TriedAddrs()); got != 2 {
		t.Errorf("TriedAddrs len = %d, want 2 (cap should evict the LRQ entry)", got)
	}
}
