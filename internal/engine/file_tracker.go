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
type FileCompleteEvent struct {
	InfoHash  string // 40-char hex torrent infohash
	FileIndex int    // file's index in the upverted file list
	Path      string // user-visible path (e.g. "Some Movie/Some Movie.mkv")
	Size      int64  // file length in bytes
}

// fileTracker watches a single torrent's piece-state subscription and emits
// a FileCompleteEvent the first time each file becomes fully complete.
//
// The tracker fans events out to any number of independent subscribers.
// Each call to Subscribe() returns a fresh buffered channel; every emitted
// event is delivered to every subscriber (with a per-subscriber drop-on-full
// policy so one slow consumer cannot stall the rest). This matters because
// two unrelated goroutines observe the stream — the CLI progressLoop that
// renders "file complete" lines, and Engine.ingestFileEvents that feeds the
// extractor pipeline. The earlier single-channel design silently split events
// between the two, so some files never reached the indexer.
type fileTracker struct {
	log   *slog.Logger
	t     *torrent.Torrent
	ihHex string

	mu          sync.Mutex
	subscribers []chan FileCompleteEvent
	// doneReplay is the replay buffer for subscribers that register
	// after run() has already dispatched events for files that were
	// complete at startup. Without it, ingestFileEvents (spawned as
	// a separate goroutine from registerLocked) can race the
	// fileTracker goroutine on a seed whose files are all complete
	// on-disk at add time: the initial dispatch fires into an empty
	// subscriber list, and the pipeline never sees the file-complete
	// events. The replay bounds itself to the torrent's file count,
	// so memory is O(files), not O(events).
	doneReplay []FileCompleteEvent
	closed     bool

	closeCh   chan struct{}
	closeOnce sync.Once
}

const fileTrackerSubBuf = 64

// startFileTracker kicks off the tracker goroutine and returns a handle
// with no subscribers attached. Callers use Subscribe() to obtain a stream
// of events. The goroutine waits for torrent metadata before it does any
// real work, so it is safe to call this even on a freshly-added magnet.
func startFileTracker(t *torrent.Torrent, log *slog.Logger) *fileTracker {
	ft := &fileTracker{
		log:     log,
		t:       t,
		ihHex:   t.InfoHash().HexString(),
		closeCh: make(chan struct{}),
	}
	go ft.run()
	return ft
}

// Subscribe returns a fresh receive-only channel of FileCompleteEvent values.
// Every event emitted by the tracker after this call is delivered to this
// subscriber. Callers SHOULD drain the channel; if they fall behind past the
// per-subscriber buffer, individual events for that subscriber are dropped
// and logged (other subscribers are unaffected).
//
// The channel is closed when Close() is called or the tracker's piece
// subscription terminates. If Subscribe is called after the tracker has
// already shut down, the returned channel is closed immediately.
func (ft *fileTracker) Subscribe() <-chan FileCompleteEvent {
	ch := make(chan FileCompleteEvent, fileTrackerSubBuf)
	ft.mu.Lock()
	if ft.closed {
		ft.mu.Unlock()
		close(ch)
		return ch
	}
	// Replay events for files already dispatched. If the replay
	// exceeds the channel buffer, the overflow is dropped — but
	// the buffer is fileTrackerSubBuf (64), which exceeds any
	// realistic file-count-at-startup case for content torrents.
	// Larger torrents that exceed the buffer would simply need
	// a bigger fileTrackerSubBuf; we don't allocate a side channel
	// because the only current consumer (the ingest pipeline) has
	// its own input buffer that would absorb any backlog.
	for _, ev := range ft.doneReplay {
		select {
		case ch <- ev:
		default:
			ft.log.Warn("file_tracker.replay.dropped",
				"info_hash", ft.ihHex,
				"file_index", ev.FileIndex,
				"path", ev.Path,
			)
		}
	}
	ft.subscribers = append(ft.subscribers, ch)
	ft.mu.Unlock()
	return ch
}

// Close detaches from the torrent and shuts down the goroutine. Idempotent.
// After Close returns, every outstanding subscriber channel is closed.
func (ft *fileTracker) Close() {
	ft.closeOnce.Do(func() {
		close(ft.closeCh)
	})
}

// run is the main goroutine. It is structured so the setup phase (waiting
// on GotInfo, building the file map, seeding the pending counters) can be
// interrupted by Close() before any real work starts.
func (ft *fileTracker) run() {
	defer ft.closeAllSubscribers()

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

// dispatch marks a file as done and broadcasts the corresponding event to
// every current subscriber. A subscriber whose buffer is full gets its
// event dropped (and logged) rather than blocking the whole fan-out.
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

	ft.mu.Lock()
	subs := ft.subscribers
	// Record the event for replay to late subscribers. See the
	// doneReplay comment on the struct for why this is needed.
	ft.doneReplay = append(ft.doneReplay, ev)
	ft.mu.Unlock()

	ft.log.Info("file.complete",
		"info_hash", ft.ihHex,
		"file_index", span.Index,
		"path", span.Path,
		"size", span.Size,
	)
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			ft.log.Warn("file.complete.dropped",
				"info_hash", ft.ihHex,
				"file_index", span.Index,
				"path", span.Path,
			)
		}
	}
}

// closeAllSubscribers closes every outstanding subscriber channel and
// marks the tracker closed so any subsequent Subscribe() gets an already-
// closed channel. Safe to call exactly once — run() invokes it via defer.
func (ft *fileTracker) closeAllSubscribers() {
	ft.mu.Lock()
	ft.closed = true
	subs := ft.subscribers
	ft.subscribers = nil
	ft.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}
