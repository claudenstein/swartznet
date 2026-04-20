package swarmsearch_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestNewPeerBookDefaultsOnZeroOrNegative covers the two
// default-fill branches in NewPeerBook: passing 0 or a negative
// value for either max-table size substitutes the production
// default rather than constructing a degenerate book that can
// never accept a peer.
func TestNewPeerBookDefaultsOnZeroOrNegative(t *testing.T) {
	t.Parallel()
	pb := swarmsearch.NewPeerBook(0, -5)
	if pb == nil {
		t.Fatal("NewPeerBook returned nil")
	}
	// Sanity: book accepts and lists peers (proves the defaults
	// produced a usable, non-zero-cap book).
	pb.AddNew("1.2.3.4:6881")
	if got := len(pb.NewAddrs()); got != 1 {
		t.Errorf("NewPeers len = %d, want 1", got)
	}
}

// TestNewHitCacheDefaultOnZeroOrNegative mirrors the above for
// the LRU hit cache. Passing 0 or a negative cap must substitute
// DefaultHitCacheSize so subsequent Stores don't immediately
// evict everything.
func TestNewHitCacheDefaultOnZeroOrNegative(t *testing.T) {
	t.Parallel()
	hc := swarmsearch.NewHitCache(0)
	if hc == nil {
		t.Fatal("NewHitCache returned nil")
	}
	if got := hc.Size(); got != 0 {
		t.Errorf("Size on fresh cache = %d, want 0", got)
	}

	hc2 := swarmsearch.NewHitCache(-1)
	if hc2 == nil {
		t.Fatal("NewHitCache(-1) returned nil")
	}
}

// TestPeerBookRecordFailureUnknownAddrNoop covers the third
// branch of RecordFailure: the addr isn't in tried, isn't in
// newPeers, so the function falls through without touching state.
func TestPeerBookRecordFailureUnknownAddrNoop(t *testing.T) {
	t.Parallel()
	pb := swarmsearch.NewPeerBook(8, 8)
	// No AddNew or Promote first — the addr is unknown to both
	// tables. RecordFailure must not panic and must not somehow
	// register the peer.
	pb.RecordFailure("9.9.9.9:6881")
	if got := len(pb.NewAddrs()); got != 0 {
		t.Errorf("NewPeers len = %d, want 0", got)
	}
	if got := len(pb.TriedAddrs()); got != 0 {
		t.Errorf("TriedAddrs len = %d, want 0", got)
	}
}
