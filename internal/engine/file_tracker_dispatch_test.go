package engine

import (
	"io"
	"log/slog"
	"testing"
)

// TestDispatchEmitsEventThenSkipsDuplicate covers the happy
// path of fileTracker.dispatch: the first call for a span pushes
// an event onto every subscriber; a second call for the same span
// is a noop (early return on the `done[span.Index]` guard).
func TestDispatchEmitsEventThenSkipsDuplicate(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex: "abcdef",
	}
	sub := ft.Subscribe()

	span := fileSpan{Index: 7, Path: "movie/video.mkv", Size: 1024}
	done := make(map[int]bool)

	ft.dispatch(span, done)
	ft.dispatch(span, done) // duplicate — must not produce a second event

	ft.closeAllSubscribers()

	var got []FileCompleteEvent
	for ev := range sub {
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

// TestDispatchDropsWhenSubscriberFull covers the select-default
// branch — a subscriber with a full buffer triggers the
// "drop and warn" path rather than blocking the whole fan-out.
func TestDispatchDropsWhenSubscriberFull(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex: "ffeedd",
	}
	// Install an already-full subscriber channel directly. Because Subscribe()
	// always returns a freshly-allocated channel at cap fileTrackerSubBuf, we
	// plug a zero-capacity one in by hand for this edge case.
	full := make(chan FileCompleteEvent)
	ft.subscribers = append(ft.subscribers, full)

	span := fileSpan{Index: 1, Path: "x.bin", Size: 5}
	done := make(map[int]bool)

	ft.dispatch(span, done)

	if !done[1] {
		t.Error("done[1] should still be true even when the event was dropped")
	}
	select {
	case ev := <-full:
		t.Errorf("unexpected event delivered: %+v", ev)
	default:
	}
}

// TestFanOutDeliversToAllSubscribers proves two subscribers each
// receive every event — the regression that motivated the refactor.
// Before fan-out, two goroutines reading the same channel would split
// events between themselves, so half would vanish from each stream.
func TestFanOutDeliversToAllSubscribers(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex: "cafebabe",
	}
	a := ft.Subscribe()
	b := ft.Subscribe()

	done := make(map[int]bool)
	spans := []fileSpan{
		{Index: 0, Path: "a.txt", Size: 1},
		{Index: 1, Path: "b.txt", Size: 2},
		{Index: 2, Path: "c.txt", Size: 3},
	}
	for _, s := range spans {
		ft.dispatch(s, done)
	}
	ft.closeAllSubscribers()

	collect := func(ch <-chan FileCompleteEvent) []int {
		var out []int
		for ev := range ch {
			out = append(out, ev.FileIndex)
		}
		return out
	}
	gotA := collect(a)
	gotB := collect(b)
	want := []int{0, 1, 2}

	eq := func(x, y []int) bool {
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i] != y[i] {
				return false
			}
		}
		return true
	}
	if !eq(gotA, want) {
		t.Errorf("subscriber A = %v, want %v", gotA, want)
	}
	if !eq(gotB, want) {
		t.Errorf("subscriber B = %v, want %v", gotB, want)
	}
}

// TestSubscribeAfterCloseReturnsClosedChannel proves a late-arriving
// caller sees a closed channel instead of hanging forever waiting for
// events that will never come.
func TestSubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex: "deadbeef",
	}
	ft.closeAllSubscribers()
	ch := ft.Subscribe()
	if _, ok := <-ch; ok {
		t.Fatal("late Subscribe() channel must start closed")
	}
}
