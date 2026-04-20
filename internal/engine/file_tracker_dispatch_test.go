package engine

import (
	"io"
	"log/slog"
	"testing"
)

// TestDispatchEmitsEventThenSkipsDuplicate covers the happy
// path of fileTracker.dispatch: the first call for a span pushes
// an event onto the channel; a second call for the same span is
// a noop (early return on the `done[span.Index]` guard).
func TestDispatchEmitsEventThenSkipsDuplicate(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex:  "abcdef",
		events: make(chan FileCompleteEvent, 4),
	}
	span := fileSpan{Index: 7, Path: "movie/video.mkv", Size: 1024}
	done := make(map[int]bool)

	ft.dispatch(span, done)
	ft.dispatch(span, done) // duplicate — must not produce a second event

	close(ft.events)

	var got []FileCompleteEvent
	for ev := range ft.events {
		got = append(got, ev)
	}
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(got))
	}
	if got[0].FileIndex != 7 || got[0].Path != "movie/video.mkv" || got[0].Size != 1024 {
		t.Errorf("event = %+v", got[0])
	}
	if got[0].InfoHash != "abcdef" {
		t.Errorf("InfoHash = %q, want \"abcdef\"", got[0].InfoHash)
	}
	if !done[7] {
		t.Error("done[7] should be true after dispatch")
	}
}

// TestDispatchDropsWhenChannelFull covers the select-default
// branch — a full events channel triggers the documented
// "drop and warn" path rather than blocking.
func TestDispatchDropsWhenChannelFull(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex:  "ffeedd",
		events: make(chan FileCompleteEvent), // unbuffered → always full
	}
	span := fileSpan{Index: 1, Path: "x.bin", Size: 5}
	done := make(map[int]bool)

	// With no reader on the unbuffered channel, the send select
	// falls through to the default case immediately.
	ft.dispatch(span, done)

	if !done[1] {
		t.Error("done[1] should still be true even when the event was dropped")
	}
	// The channel must still be empty (no event ever landed).
	select {
	case ev := <-ft.events:
		t.Errorf("unexpected event delivered: %+v", ev)
	default:
	}
}
