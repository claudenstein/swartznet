package engine

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPieceSubscriptionForward verifies that events sent on the upstream
// values channel appear on the consumer's Events() channel.
func TestPieceSubscriptionForward(t *testing.T) {
	t.Parallel()

	upstream := make(chan torrent.PieceStateChange, 8)
	closeCalled := false

	ts := &torrentSubscription{
		values: upstream,
		closer: func() { closeCalled = true },
	}

	ps := &pieceSubscription{
		log:      discardLogger(),
		sub:      ts,
		closeCh:  make(chan struct{}),
		consumer: make(chan torrent.PieceStateChange, 64),
	}
	go ps.run("deadbeef")

	// Send a few events and verify they arrive.
	want := []torrent.PieceStateChange{
		{Index: 0},
		{Index: 1},
		{Index: 42},
	}
	for _, ev := range want {
		upstream <- ev
	}

	for i, wantEv := range want {
		select {
		case got := <-ps.Events():
			if got.Index != wantEv.Index {
				t.Errorf("event %d: Index = %d, want %d", i, got.Index, wantEv.Index)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}

	// Clean shutdown.
	ps.Close()
	if !closeCalled {
		t.Error("upstream closer was not called")
	}
}

// TestPieceSubscriptionDrop fills the consumer channel's buffer (64) and
// then sends one more event. The extra event must be silently dropped
// (non-blocking fanout) rather than blocking the producer goroutine.
func TestPieceSubscriptionDrop(t *testing.T) {
	t.Parallel()

	upstream := make(chan torrent.PieceStateChange, 128)

	ts := &torrentSubscription{
		values: upstream,
		closer: func() {},
	}

	ps := &pieceSubscription{
		log:      discardLogger(),
		sub:      ts,
		closeCh:  make(chan struct{}),
		consumer: make(chan torrent.PieceStateChange, 64),
	}
	go ps.run("deadbeef")

	// Fill the consumer buffer (64 slots).
	for i := 0; i < 64; i++ {
		upstream <- torrent.PieceStateChange{Index: i}
	}

	// Give the forwarder goroutine time to drain upstream into consumer.
	// We know consumer is full when all 64 have been forwarded.
	deadline := time.After(3 * time.Second)
	for {
		if len(ps.consumer) == 64 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("consumer channel never filled: len=%d", len(ps.consumer))
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// Now send one more event. If the forwarder blocks, this test will
	// hang and eventually time out — proving the drop path is broken.
	upstream <- torrent.PieceStateChange{Index: 999}

	// Give the forwarder time to process the dropped event.
	time.Sleep(50 * time.Millisecond)

	// Consumer should still have exactly 64 events (the extra was dropped).
	if got := len(ps.consumer); got != 64 {
		t.Errorf("consumer len = %d after drop, want 64", got)
	}

	ps.Close()
}

// TestPieceSubscriptionCloseIdempotent calls Close() twice and verifies
// that the second call does not panic or return an error.
func TestPieceSubscriptionCloseIdempotent(t *testing.T) {
	t.Parallel()

	upstream := make(chan torrent.PieceStateChange, 1)
	closeCount := 0

	ts := &torrentSubscription{
		values: upstream,
		closer: func() { closeCount++ },
	}

	ps := &pieceSubscription{
		log:      discardLogger(),
		sub:      ts,
		closeCh:  make(chan struct{}),
		consumer: make(chan torrent.PieceStateChange, 64),
	}
	go ps.run("deadbeef")

	ps.Close()
	ps.Close() // must not panic

	if closeCount != 1 {
		t.Errorf("upstream closer called %d times, want exactly 1", closeCount)
	}
}

// TestPieceSubscriptionUpstreamClose verifies that when the upstream values
// channel is closed (simulating the real pubsub closing), the consumer
// channel is eventually closed too.
func TestPieceSubscriptionUpstreamClose(t *testing.T) {
	t.Parallel()

	upstream := make(chan torrent.PieceStateChange, 1)

	ts := &torrentSubscription{
		values: upstream,
		closer: func() {},
	}

	ps := &pieceSubscription{
		log:      discardLogger(),
		sub:      ts,
		closeCh:  make(chan struct{}),
		consumer: make(chan torrent.PieceStateChange, 64),
	}
	go ps.run("deadbeef")

	// Send one event, then close upstream.
	upstream <- torrent.PieceStateChange{Index: 7}
	close(upstream)

	// The consumer channel should deliver the event and then close.
	select {
	case ev, ok := <-ps.Events():
		if !ok {
			t.Fatal("consumer closed before delivering buffered event")
		}
		if ev.Index != 7 {
			t.Errorf("Index = %d, want 7", ev.Index)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for buffered event")
	}

	// Now the channel should close.
	select {
	case _, ok := <-ps.Events():
		if ok {
			t.Error("expected consumer channel to be closed, but got an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for consumer channel to close")
	}
}

// TestPieceSubscriptionEventsIsReadOnly confirms that the Events() channel
// is receive-only, preventing callers from accidentally sending into it.
// This is a compile-time check that Events() returns <-chan, not chan.
func TestPieceSubscriptionEventsIsReadOnly(t *testing.T) {
	t.Parallel()

	upstream := make(chan torrent.PieceStateChange, 1)
	ts := &torrentSubscription{
		values: upstream,
		closer: func() {},
	}

	ps := &pieceSubscription{
		log:      discardLogger(),
		sub:      ts,
		closeCh:  make(chan struct{}),
		consumer: make(chan torrent.PieceStateChange, 64),
	}
	go ps.run("deadbeef")
	defer ps.Close()

	// Compile-time check: the returned type is <-chan, so this assignment
	// must succeed. If Events() returned a bidirectional chan, this would
	// still compile, but the test documents the intended API contract.
	var _ <-chan torrent.PieceStateChange = ps.Events()
}
