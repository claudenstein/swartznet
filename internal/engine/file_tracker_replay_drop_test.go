package engine

import (
	"io"
	"log/slog"
	"testing"
)

// TestFileTrackerSubscribeReplayDrops covers fileTracker.Subscribe's
// `default: log.Warn("file_tracker.replay.dropped")` arm at
// file_tracker.go:100-105. The arm fires when the replay buffer
// holds more events than the per-subscriber channel buffer (64).
// Construct a tracker, seed doneReplay with 65 events directly,
// then Subscribe — the 65th event has nowhere to go and trips
// the drop path.
func TestFileTrackerSubscribeReplayDrops(t *testing.T) {
	t.Parallel()
	ft := &fileTracker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		ihHex: "11111111111111111111",
	}
	// 65 events: one more than fileTrackerSubBuf.
	for i := 0; i < fileTrackerSubBuf+1; i++ {
		ft.doneReplay = append(ft.doneReplay, FileCompleteEvent{
			FileIndex: i,
			Path:      "fake",
		})
	}

	ch := ft.Subscribe()
	// Drain so we don't block at GC time. We expect exactly
	// fileTrackerSubBuf events made it through; the 65th was
	// dropped via the default-case arm.
	got := 0
	for got < fileTrackerSubBuf {
		select {
		case <-ch:
			got++
		default:
			t.Fatalf("subscriber drained early at %d events, want %d", got, fileTrackerSubBuf)
		}
	}
	// One more should not be in the buffer (it was dropped).
	select {
	case <-ch:
		t.Errorf("expected the 65th event to be dropped, but received it")
	default:
	}
}
