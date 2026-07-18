package engine

import (
	"context"
	"log/slog"
	"sync"

	"github.com/anacrolix/torrent"
)

// fileTrackerSubBuf is each subscriber's channel capacity; a full buffer
// drops that subscriber's event, never blocks the tracker.
const fileTrackerSubBuf = 64

// FileCompleteEvent fires exactly once per completed file.
type FileCompleteEvent struct {
	InfoHash  string // 40-hex
	FileIndex int
	Path      string
	Size      int64
}

// fileTracker watches piece completion and dispatches exactly one
// FileCompleteEvent per file, fanning out to every subscriber with a
// done-replay buffer for late subscribers (a seed complete-on-disk at add
// time dispatches before any consumer's subscription lands).
type fileTracker struct {
	t   *torrent.Torrent
	log *slog.Logger

	mu         sync.Mutex
	subs       []chan FileCompleteEvent
	doneReplay []FileCompleteEvent
	closed     bool

	closeOnce sync.Once
	stop      chan struct{}
}

type trackedFile struct {
	index      int
	path       string
	size       int64
	begin, end int // half-open piece span [begin, end)
	remaining  int
	done       bool
}

func startFileTracker(t *torrent.Torrent, bgCtx context.Context, log *slog.Logger) *fileTracker {
	ft := &fileTracker{t: t, log: log, stop: make(chan struct{})}
	go ft.run(bgCtx)
	return ft
}

// Subscribe returns a fresh buffered channel and replays already-dispatched
// events into it first. After Close it returns an already-closed channel.
func (ft *fileTracker) Subscribe() <-chan FileCompleteEvent {
	ch := make(chan FileCompleteEvent, fileTrackerSubBuf)
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if ft.closed {
		close(ch)
		return ch
	}
	for _, ev := range ft.doneReplay {
		select {
		case ch <- ev:
		default:
			ft.log.Warn("file_tracker.replay.dropped", "info_hash", ev.InfoHash, "file_index", ev.FileIndex, "path", ev.Path)
		}
	}
	ft.subs = append(ft.subs, ch)
	return ch
}

// Close is idempotent; every subscriber channel is closed.
func (ft *fileTracker) Close() {
	ft.closeOnce.Do(func() {
		close(ft.stop)
		ft.mu.Lock()
		ft.closed = true
		for _, ch := range ft.subs {
			close(ch)
		}
		ft.subs = nil
		ft.mu.Unlock()
	})
}

func (ft *fileTracker) dispatch(ev FileCompleteEvent) {
	ft.log.Info("file.complete", "info_hash", ev.InfoHash, "file_index", ev.FileIndex, "path", ev.Path, "size", ev.Size)
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if ft.closed {
		return
	}
	ft.doneReplay = append(ft.doneReplay, ev)
	for _, ch := range ft.subs {
		select {
		case ch <- ev:
		default:
			ft.log.Warn("file.complete.dropped", "info_hash", ev.InfoHash, "file_index", ev.FileIndex, "path", ev.Path)
		}
	}
}

func (ft *fileTracker) run(bgCtx context.Context) {
	select {
	case <-ft.t.GotInfo():
	case <-ft.stop:
		return
	case <-bgCtx.Done():
		return
	}

	ihHex := ft.t.InfoHash().HexString()
	numPieces := ft.t.NumPieces()
	files := make([]*trackedFile, 0, len(ft.t.Files()))
	for i, f := range ft.t.Files() {
		begin, end := f.BeginPieceIndex(), f.EndPieceIndex()
		if begin < 0 {
			begin = 0
		}
		if end > numPieces {
			end = numPieces
		}
		files = append(files, &trackedFile{
			index: i,
			path:  f.DisplayPath(),
			size:  f.Length(),
			begin: begin,
			end:   end,
		})
	}

	// Subscribe BEFORE seeding the remaining-counters from current state —
	// the reverse order loses pieces completing in between (TOCTOU).
	sub := ft.t.SubscribePieceStateChanges()
	defer sub.Close()

	pieceDone := make([]bool, numPieces)
	for i := 0; i < numPieces; i++ {
		st := ft.t.PieceState(i)
		pieceDone[i] = st.Complete && st.Ok
	}
	for _, tf := range files {
		tf.remaining = 0
		for p := tf.begin; p < tf.end; p++ {
			if !pieceDone[p] {
				tf.remaining++
			}
		}
		if tf.remaining == 0 && !tf.done {
			tf.done = true // zero-length files are immediately done too
			ft.dispatch(FileCompleteEvent{InfoHash: ihHex, FileIndex: tf.index, Path: tf.path, Size: tf.size})
		}
	}

	for {
		select {
		case ev, ok := <-sub.Values:
			if !ok {
				return
			}
			idx := ev.Index
			complete := ev.Complete && ev.Ok
			if idx < 0 || idx >= numPieces || pieceDone[idx] == complete {
				continue
			}
			pieceDone[idx] = complete
			delta := -1
			if !complete {
				delta = 1
			}
			for _, tf := range files {
				if tf.done || idx < tf.begin || idx >= tf.end {
					continue
				}
				tf.remaining += delta
				if tf.remaining == 0 {
					tf.done = true
					ft.dispatch(FileCompleteEvent{InfoHash: ihHex, FileIndex: tf.index, Path: tf.path, Size: tf.size})
				}
			}
		case <-ft.stop:
			return
		case <-bgCtx.Done():
			return
		}
	}
}

// pieceSubscription forwards anacrolix piece-state changes into an owned
// channel (buffer 64, drop-on-full at Debug). Consumers must drain.
type pieceSubscription struct {
	events    chan int
	stop      chan struct{}
	closeOnce sync.Once
}

func startPieceSubscription(t *torrent.Torrent, log *slog.Logger) *pieceSubscription {
	ps := &pieceSubscription{
		events: make(chan int, 64),
		stop:   make(chan struct{}),
	}
	go func() {
		// The consumer channel closes on EVERY forwarder exit — including a
		// Close before metadata ever arrives — so drainers never hang.
		defer close(ps.events)
		select {
		case <-t.GotInfo():
		case <-ps.stop:
			return
		}
		sub := t.SubscribePieceStateChanges()
		defer sub.Close()
		ihHex := t.InfoHash().HexString()
		for {
			select {
			case ev, ok := <-sub.Values:
				if !ok {
					return
				}
				select {
				case ps.events <- ev.Index:
				default:
					log.Debug("piece.sub.drop", "info_hash", ihHex, "piece", ev.Index)
				}
			case <-ps.stop:
				return
			}
		}
	}()
	return ps
}

func (ps *pieceSubscription) Close() {
	ps.closeOnce.Do(func() { close(ps.stop) })
}
