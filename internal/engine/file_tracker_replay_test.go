package engine

import (
	"io"
	"log/slog"
	"testing"
)

// TestSubscribeReplaysPreDispatchedEvents reproduces the race observed
// against a seeded torrent whose pieces are already complete at
// Engine.AddTorrentFile time: the fileTracker goroutine's initial
// dispatch fires into an empty subscriber list (ingestFileEvents hasn't
// yet subscribed), the pipeline sees no file-complete events, and
// content extraction never runs.
//
// The fix is a per-tracker replay buffer: dispatch() appends the event
// to ft.doneReplay, and Subscribe() copies the buffer into the new
// subscriber's channel before returning it. A subscriber registering
// after the burst therefore still receives every event.
func TestSubscribeReplaysPreDispatchedEvents(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex: "cafef00d",
	}

	// Dispatch three files BEFORE any subscriber is registered —
	// the exact condition that happens on seeds.
	done := make(map[int]bool)
	ft.dispatch(fileSpan{Index: 0, Path: "a.txt", Size: 10}, done)
	ft.dispatch(fileSpan{Index: 1, Path: "b.txt", Size: 20}, done)
	ft.dispatch(fileSpan{Index: 2, Path: "c.txt", Size: 30}, done)

	// Late subscriber — must still observe every event.
	sub := ft.Subscribe()
	ft.closeAllSubscribers()

	var got []FileCompleteEvent
	for ev := range sub {
		got = append(got, ev)
	}
	if len(got) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(got))
	}
	for i, ev := range got {
		if ev.FileIndex != i {
			t.Errorf("events[%d].FileIndex = %d, want %d", i, ev.FileIndex, i)
		}
		if ev.InfoHash != "cafef00d" {
			t.Errorf("events[%d].InfoHash = %q, want \"cafef00d\"", i, ev.InfoHash)
		}
	}
}

// TestSubscribeReplayDoesNotDoubleDeliver guards the live-plus-replay
// combination: a subscriber registered mid-stream must see the replayed
// events exactly once and the live events exactly once — no duplicates.
func TestSubscribeReplayDoesNotDoubleDeliver(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex: "1234abcd",
	}
	done := make(map[int]bool)
	// First two fire BEFORE Subscribe — they land in replay only.
	ft.dispatch(fileSpan{Index: 0, Path: "x.txt", Size: 1}, done)
	ft.dispatch(fileSpan{Index: 1, Path: "y.txt", Size: 2}, done)

	sub := ft.Subscribe()

	// Next two fire AFTER Subscribe — they land via the live fan-out.
	ft.dispatch(fileSpan{Index: 2, Path: "z.txt", Size: 3}, done)
	ft.dispatch(fileSpan{Index: 3, Path: "w.txt", Size: 4}, done)

	ft.closeAllSubscribers()

	var got []FileCompleteEvent
	for ev := range sub {
		got = append(got, ev)
	}
	if len(got) != 4 {
		t.Fatalf("len(events) = %d, want 4: %+v", len(got), got)
	}
	seen := make(map[int]int)
	for _, ev := range got {
		seen[ev.FileIndex]++
	}
	for i := 0; i < 4; i++ {
		if seen[i] != 1 {
			t.Errorf("FileIndex %d delivered %d times, want 1", i, seen[i])
		}
	}
}
