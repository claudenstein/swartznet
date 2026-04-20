package swarmsearch_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestPeerBookPromoteUnknownAddrNoop covers the
// `!ok → return` branch of Promote — calling Promote on an
// address that's in neither the tried nor the new table must
// be a noop. The other Promote tests exercise the tried-already
// and new→tried-promotion branches.
func TestPeerBookPromoteUnknownAddrNoop(t *testing.T) {
	t.Parallel()
	pb := swarmsearch.NewPeerBook(8, 8)
	pb.Promote("8.8.8.8:6881")
	if got := len(pb.NewAddrs()); got != 0 {
		t.Errorf("NewAddrs len = %d, want 0", got)
	}
	if got := len(pb.TriedAddrs()); got != 0 {
		t.Errorf("TriedAddrs len = %d, want 0", got)
	}
}
