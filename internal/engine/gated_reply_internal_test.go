package engine

import (
	"errors"
	"testing"
	"time"
)

// TestGatedReplyWriterDropsWhenSlotsExhausted covers the admission
// control added to the inbound sn_search reply path: when every
// writer slot is busy, reply() must drop the body and return
// errReplyOverloaded instead of spawning yet another goroutine —
// remote input must not translate into unbounded writer growth.
func TestGatedReplyWriterDropsWhenSlotsExhausted(t *testing.T) {
	t.Parallel()
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // exhaust the only slot

	wrote := make(chan []byte, 1)
	reply := gatedReplyWriter(sem,
		func(body []byte) error {
			wrote <- body
			return nil
		},
		func(error) {})

	if err := reply([]byte("dropped")); !errors.Is(err, errReplyOverloaded) {
		t.Fatalf("reply with full sem: err = %v, want errReplyOverloaded", err)
	}
	select {
	case b := <-wrote:
		t.Fatalf("write ran despite drop: %q", b)
	case <-time.After(50 * time.Millisecond):
	}

	// Free the slot: the next reply must go through, and must write
	// a COPY of the body (the protocol layer reuses its buffer).
	<-sem
	body := []byte("sent")
	if err := reply(body); err != nil {
		t.Fatalf("reply with free sem: %v", err)
	}
	body[0] = 'X' // mutate after reply returns
	select {
	case b := <-wrote:
		if string(b) != "sent" {
			t.Errorf("write saw %q, want %q (body must be copied)", b, "sent")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write goroutine never ran after slot freed")
	}
}

// TestGatedReplyWriterSurfacesWriteErr covers the asynchronous error
// path: a failing write must be reported via onErr (the engine logs
// it at debug), not lost.
func TestGatedReplyWriterSurfacesWriteErr(t *testing.T) {
	t.Parallel()
	sem := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	reply := gatedReplyWriter(sem,
		func([]byte) error { return errors.New("boom") },
		func(err error) { errCh <- err })

	if err := reply([]byte("x")); err != nil {
		t.Fatalf("reply: %v", err)
	}
	select {
	case err := <-errCh:
		if err == nil || err.Error() != "boom" {
			t.Errorf("onErr got %v, want boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onErr never invoked for failing write")
	}
}
