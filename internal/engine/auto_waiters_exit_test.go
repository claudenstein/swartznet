package engine

import (
	"crypto/rand"
	"testing"
	"time"
)

// addInfoOnlyHandle adds a random bare infohash to the engine. With
// DHT disabled and no peers, metadata never arrives, so GotInfo
// stays open — exactly the state in which the metadata-wait
// goroutines used to park on their 5–10 minute timers after Close.
func addInfoOnlyHandle(t *testing.T, eng *Engine) *Handle {
	t.Helper()
	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	h, err := eng.AddInfoHash(ih)
	if err != nil {
		t.Fatalf("AddInfoHash: %v", err)
	}
	return h
}

// TestMetadataWaitersExitOnShutdown covers the e.bgCtx.Done() select
// arms added to autoDownload, autoIndex and upgradeMagnetSession:
// cancelling the engine background context must release each
// goroutine promptly instead of leaking it on the metadata timer
// (5 min for autoDownload/autoIndex, 10 min for
// upgradeMagnetSession) long after Close returned.
func TestMetadataWaitersExitOnShutdown(t *testing.T) {
	t.Parallel()
	eng := newPausedSkipEngine(t)
	h := addInfoOnlyHandle(t, eng)

	waiters := map[string]func(*Handle){
		"autoDownload":         eng.autoDownload,
		"autoIndex":            eng.autoIndex,
		"upgradeMagnetSession": eng.upgradeMagnetSession,
	}
	done := make(map[string]chan struct{}, len(waiters))
	for name, fn := range waiters {
		ch := make(chan struct{})
		done[name] = ch
		go func(fn func(*Handle), ch chan struct{}) {
			fn(h)
			close(ch)
		}(fn, ch)
	}

	eng.bgCancel()

	for name, ch := range done {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Errorf("%s did not exit after bgCtx cancel", name)
		}
	}
}
