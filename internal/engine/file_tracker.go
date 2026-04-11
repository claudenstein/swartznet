package engine

import (
	"log/slog"
	"sync"

	"github.com/anacrolix/torrent"
)

// FileCompleteEvent is emitted by a fileTracker the first time all pieces
// covering a given file are verified complete. It is the input signal M2.2's
// text-extractor pipeline will react to: "this file is fully on disk now, go
// extract its content and feed the Bleve index."
//
// M2.1 delivers only the event stream; M2.2 will attach a consumer.
type FileCompleteEvent struct {
	InfoHash  string // 40-char hex torrent infohash
	FileIndex int    // file's index in the upverted file list
	Path      string // user-visible path (e.g. "Some Movie/Some Movie.mkv")
	Size      int64  // file length in bytes
}

// fileTracker watches a single torrent's piece-state subscription and emits
// a FileCompleteEvent the first time each file becomes fully complete.
//
// Lifecycle:
//  1. Caller constructs via startFileTracker, which returns immediately.
//  2. A background goroutine waits for GotInfo, builds the file map, seeds
//     the remaining-piece counters from current piece state, subscribes to
//     future piece changes, and runs the main loop.
//  3. Consumers read events from Events() until Close() or the torrent's
//     piece subscription closes.
//
// Close is safe to call multiple times and unblocks the main loop even if
// it is still waiting on GotInfo.
type fileTracker struct {
	log       *slog.Logger
	t         *torrent.Torrent
	ihHex     string
	events    chan FileCompleteEvent
	closeCh   chan struct{}
	closeOnce sync.Once
}

// startFileTracker kicks off the tracker goroutine and returns a handle
// with a buffered events channel. The goroutine waits for torrent metadata
// before it does any real work, so it is safe to call this even on a
// freshly-added magnet.
func startFileTracker(t *torrent.Torrent, log *slog.Logger) *fileTracker {
	ft := &fileTracker{
		log:     log,
		t:       t,
		ihHex:   t.InfoHash().HexString(),
		events:  make(chan FileCompleteEvent, 64),
		closeCh: make(chan struct{}),
	}
	go ft.run()
	return ft
}

// Events returns the channel of file-complete events. It is closed when
// Close() is called or when the underlying piece subscription terminates.
// Consumers SHOULD drain the channel; if they fall behind, individual events
// are dropped (see the select in dispatch below).
func (ft *fileTracker) Events() <-chan FileCompleteEvent {
	return ft.events
}

// Close detaches from the torrent and shuts down the goroutine. Idempotent.
func (ft *fileTracker) Close() {
	ft.closeOnce.Do(func() {
		close(ft.closeCh)
	})
}

// run is the main goroutine. It is structured so the setup phase (waiting
// on GotInfo, building the file map, seeding the pending counters) can be
// interrupted by Close() before any real work starts.
func (ft *fileTracker) run() {
	defer close(ft.events)

	// Wait for torrent metadata, interruptible by Close().
	select {
	case <-ft.t.GotInfo():
	case <-ft.closeCh:
		return
	}

	fm, err := buildFileMap(ft.t.Info())
	if err != nil {
		ft.log.Warn("file_tracker.buildFileMap", "info_hash", ft.ihHex, "err", err)
		return
	}

	// Subscribe BEFORE we seed the pending counters from the current state.
	// Doing it in the other order risks missing events that arrive between
	// snapshot and subscribe (the usual TOCTOU hazard for state + event-stream
	// APIs).
	sub := ft.t.SubscribePieceStateChanges()
	defer sub.Close()

	// remaining[fileIndex] = count of still-pending pieces for that file.
	// A file whose counter reaches 0 is immediately complete, and we emit
	// exactly once using the done[] set.
	remaining := make(map[int]int, len(fm.Files()))
	done := make(map[int]bool, len(fm.Files()))

	// Seed from the torrent's current piece state. Any piece already marked
	// complete counts against the remaining set.
	for _, span := range fm.Files() {
		r := 0
		for p := span.BeginPiece; p < span.EndPiece; p++ {
			st := ft.t.Piece(p).State().Completion
			if !(st.Complete && st.Ok) {
				r++
			}
		}
		remaining[span.Index] = r
		if r == 0 {
			// Already complete at subscription time — fire the event now.
			// This covers resumed downloads and torrents whose pieces were
			// already on disk when we attached.
			ft.dispatch(span, done)
		}
	}

	// Main loop: consume piece events until the subscription closes or we
	// are told to shut down.
	for {
		select {
		case <-ft.closeCh:
			return
		case ev, ok := <-sub.Values:
			if !ok {
				return
			}
			if !(ev.Completion.Complete && ev.Completion.Ok) {
				// Ignore transient states: hashing, partial, etc. Only
				// verified-good pieces count toward file completion.
				continue
			}
			for _, fi := range fm.FilesForPiece(ev.Index) {
				if done[fi] {
					continue
				}
				if remaining[fi] > 0 {
					remaining[fi]--
					if remaining[fi] == 0 {
						ft.dispatch(fm.Files()[fi], done)
					}
				}
			}
		}
	}
}

// dispatch marks a file as done and pushes the corresponding event onto the
// output channel. If the consumer is backed up and the channel is full, the
// event is dropped and logged — M2.2's indexer will replace this with a
// proper bounded-queue + retry strategy.
func (ft *fileTracker) dispatch(span fileSpan, done map[int]bool) {
	if done[span.Index] {
		return
	}
	done[span.Index] = true
	ev := FileCompleteEvent{
		InfoHash:  ft.ihHex,
		FileIndex: span.Index,
		Path:      span.Path,
		Size:      span.Size,
	}
	select {
	case ft.events <- ev:
		ft.log.Info("file.complete",
			"info_hash", ft.ihHex,
			"file_index", span.Index,
			"path", span.Path,
			"size", span.Size,
		)
	default:
		ft.log.Warn("file.complete.dropped",
			"info_hash", ft.ihHex,
			"file_index", span.Index,
			"path", span.Path,
		)
	}
}
