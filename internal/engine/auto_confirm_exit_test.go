package engine

import (
	"testing"
	"time"
)

// TestAutoConfirmOnCompleteExitsOnRemove covers the h.removed select
// arm added to autoConfirmOnComplete: dropping a torrent via
// RemoveTorrent (which closes h.removed) must release the goroutine
// promptly instead of leaving it parked on completion / the 24h
// timer.
func TestAutoConfirmOnCompleteExitsOnRemove(t *testing.T) {
	t.Parallel()
	eng := newPausedSkipEngine(t)
	h := addLocalTorrentWithFiles(t, eng)

	done := make(chan struct{})
	go func() {
		eng.autoConfirmOnComplete(h)
		close(done)
	}()

	// The torrent's payload is already on disk; VerifyData may make
	// it complete on its own. To exercise the removed arm specifically
	// we still close removed and assert prompt exit either way.
	h.markRemoved()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("autoConfirmOnComplete did not exit after markRemoved")
	}
}

// TestAutoConfirmOnCompleteExitsOnShutdown covers the e.bgCtx.Done()
// select arm: cancelling the engine background context releases the
// goroutine without waiting on completion or the 24h timer.
func TestAutoConfirmOnCompleteExitsOnShutdown(t *testing.T) {
	t.Parallel()
	eng := newPausedSkipEngine(t)
	h := addLocalTorrentWithFiles(t, eng)

	done := make(chan struct{})
	go func() {
		eng.autoConfirmOnComplete(h)
		close(done)
	}()

	eng.bgCancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("autoConfirmOnComplete did not exit after bgCtx cancel")
	}
}
