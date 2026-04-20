package swarmsearch_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestStartFeelerStopsOnContextCancel exercises the feeler
// goroutine's lifecycle: StartFeeler launches, the ticker fires
// (or doesn't), and feelerLoop returns when ctx is cancelled.
// We use a short interval so at least one feelerOnce iteration
// likely runs against the empty peer book.
func TestStartFeelerStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	p.StartFeeler(ctx, 10*time.Millisecond)

	// Give the loop a tick or two with an empty PeerBook (every
	// feelerOnce returns early via the NewAddrs() == 0 check).
	time.Sleep(40 * time.Millisecond)

	// Cancel — the goroutine should observe ctx.Done and exit.
	cancel()
	// We can't directly observe goroutine exit, but a tiny grace
	// period plus the lack of any panic / data race is enough.
	time.Sleep(20 * time.Millisecond)
}

// TestStartFeelerWithKnownPeerExercisesQueryPath gives the peer
// book one new address so feelerOnce skips its early-out and
// actually calls Query. The query has no Sender wired so it
// returns "no peers" immediately, but the code path runs.
func TestStartFeelerWithKnownPeerExercisesQueryPath(t *testing.T) {
	t.Parallel()
	p := swarmsearch.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Pre-load one new address so NewAddrs() returns non-empty.
	p.PeerBook().AddNew("203.0.113.1:6881")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	p.StartFeeler(ctx, 10*time.Millisecond)
	<-ctx.Done()
}
