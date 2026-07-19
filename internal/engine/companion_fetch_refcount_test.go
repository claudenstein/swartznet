package engine

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestCompanionFetchRefcountDefersDrop pins the round-5 LOW fix: two concurrent
// FetchCompanionTorrent calls for the same infohash share ONE companion handle
// (anacrolix dedupes by infohash). The shared torrent must not be dropped until
// the LAST fetcher exits — an unconditional per-fetcher drop would tear the
// torrent down mid-download for the other, degrading a valid aggregate lookup to
// no-results.
func TestCompanionFetchRefcountDefersDrop(t *testing.T) {
	e := testEngine(t)

	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i + 1)
	}
	hash := metainfo.Hash(ih)
	if _, err := e.addCompanionInfoHash(hash); err != nil {
		t.Fatal(err)
	}

	// Two concurrent fetchers retain the shared handle.
	e.retainCompanionFetch(ih)
	e.retainCompanionFetch(ih)

	// First fetcher exits: the shared torrent must survive for the second.
	e.releaseCompanionFetch(ih)
	if _, err := e.handleByHex(hash.HexString()); err != nil {
		t.Fatal("companion torrent dropped while a concurrent fetch was still in progress")
	}

	// Last fetcher exits: now it is dropped (no handle/goroutine leak).
	e.releaseCompanionFetch(ih)
	if _, err := e.handleByHex(hash.HexString()); err == nil {
		t.Error("companion torrent not dropped after the last fetcher released")
	}
}
