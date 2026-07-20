package engine

import (
	"sync"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// TestCompanionFetchRefcountDefersDrop pins the round-5 fix + the round-6 TOCTOU
// hardening: two concurrent FetchCompanionTorrent calls for the same infohash
// share ONE companion handle; the shared torrent must not be dropped until the
// LAST fetcher exits, and a re-attach after a drop must get a fresh LIVE handle
// (never the torn-down one).
func TestCompanionFetchRefcountDefersDrop(t *testing.T) {
	e := testEngine(t)

	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i + 1)
	}
	hash := metainfo.Hash(ih)

	// Two concurrent fetchers retain+attach the shared handle.
	h1, err := e.retainAndAttachCompanion(ih)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := e.retainAndAttachCompanion(ih)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("concurrent fetches of one infohash did not share a handle")
	}

	// First fetcher exits: the shared torrent must survive for the second.
	e.releaseCompanionFetch(ih)
	if _, err := e.handleByHex(hash.HexString()); err != nil {
		t.Fatal("companion torrent dropped while a concurrent fetch was still in progress")
	}
	if h1.isRemoved() {
		t.Fatal("shared handle marked removed while a fetcher still held it")
	}

	// Last fetcher exits: now it is dropped (no handle/goroutine leak).
	e.releaseCompanionFetch(ih)
	if _, err := e.handleByHex(hash.HexString()); err == nil {
		t.Error("companion torrent not dropped after the last fetcher released")
	}

	// A fresh fetch after the drop must re-add a LIVE handle, never resurrect the
	// torn-down one (the round-6 TOCTOU: attach-vs-drop is atomic under the lock).
	h3, err := e.retainAndAttachCompanion(ih)
	if err != nil {
		t.Fatal(err)
	}
	if h3.isRemoved() {
		t.Error("re-attach after drop returned a torn-down handle")
	}
	e.releaseCompanionFetch(ih)
}

// TestCompanionFetchRefcountConcurrent stresses the retain/attach vs release/drop
// interleaving under -race: many goroutines fetch+release the same infohash. The
// invariant is that it never panics/races and ends clean (handle dropped, ref
// map empty) — no leaked handle, no negative refcount.
func TestCompanionFetchRefcountConcurrent(t *testing.T) {
	e := testEngine(t)
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(0x40 + i)
	}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				h, err := e.retainAndAttachCompanion(ih)
				if err != nil {
					continue
				}
				_ = h.InfoHashHex()
				e.releaseCompanionFetch(ih)
			}
		}()
	}
	wg.Wait()

	e.companionFetchMu.Lock()
	n := e.companionFetchRefs[ih]
	e.companionFetchMu.Unlock()
	if n != 0 {
		t.Errorf("refcount leaked: %d (want 0)", n)
	}
	if _, err := e.handleByHex(metainfo.Hash(ih).HexString()); err == nil {
		t.Error("companion handle leaked after all fetchers released")
	}
}
