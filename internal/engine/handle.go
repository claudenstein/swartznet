package engine

import (
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

// Handle is the engine's per-torrent bookkeeping around an anacrolix
// *torrent.Torrent.
type Handle struct {
	T   *torrent.Torrent
	eng *Engine

	mu         sync.Mutex
	paused     bool
	queued     bool
	indexing   bool // per-torrent indexing toggle, default on
	queueOrder int64
	signedBy   string

	removed    chan struct{}
	removeOnce sync.Once

	pieceSub *pieceSubscription
	fileSub  *fileTracker

	rateMu     sync.Mutex
	rateSeeded bool
	rateAt     time.Time
	rateRead   int64
	rateWrite  int64
	rateDown   int64
	rateUp     int64
}

// InfoHashHex returns the 40-hex lowercase infohash.
func (h *Handle) InfoHashHex() string { return h.T.InfoHash().HexString() }

// SignedBy returns the verified publisher pubkey hex ("" when unsigned).
func (h *Handle) SignedBy() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.signedBy
}

func (h *Handle) isPaused() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.paused
}

// setPaused flips the paused flag; reports whether the state changed.
func (h *Handle) setPaused(v bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.paused == v {
		return false
	}
	h.paused = v
	return true
}

func (h *Handle) isQueued() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.queued
}

func (h *Handle) setQueued(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queued = v
}

func (h *Handle) isIndexing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.indexing
}

func (h *Handle) getQueueOrder() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.queueOrder
}

func (h *Handle) setQueueOrder(v int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queueOrder = v
}

// markRemoved closes the removed channel exactly once.
func (h *Handle) markRemoved() {
	h.removeOnce.Do(func() { close(h.removed) })
}

// PieceEvents returns the piece-state change stream (buffer 64,
// drop-on-full). Readers must drain.
func (h *Handle) PieceEvents() <-chan int { return h.pieceSub.events }

// SubscribeFileEvents returns a NEW file-completion subscription with the
// done-replay prefix. Callers bind ONCE outside any select loop — every call
// creates a fresh subscription, so calling it inside a loop hangs forever.
func (h *Handle) SubscribeFileEvents() <-chan FileCompleteEvent {
	return h.fileSub.Subscribe()
}

// FileEvents is an explicit alias of SubscribeFileEvents, kept for parity
// with the legacy API. Same one-call rule applies.
func (h *Handle) FileEvents() <-chan FileCompleteEvent { return h.SubscribeFileEvents() }

// registerLocked registers t (caller holds e.mu). Duplicate adds of the same
// infohash return the existing handle with existed=true — callers skip their
// side effects. paused is applied before any goroutine spawns, so a
// restored-paused torrent can never race into Normal priority.
func (e *Engine) registerLocked(t *torrent.Torrent, paused bool) (h *Handle, existed bool) {
	if h, ok := e.handles[t.InfoHash()]; ok {
		return h, true
	}
	e.nextQueueOrder++
	h = &Handle{
		T:          t,
		eng:        e,
		paused:     paused,
		indexing:   true,
		queueOrder: e.nextQueueOrder,
		removed:    make(chan struct{}),
		pieceSub:   startPieceSubscription(t, e.log),
		fileSub:    startFileTracker(t, e.bgCtx, e.log),
	}
	e.handles[t.InfoHash()] = h
	go e.autoDownload(h)
	go e.watchCompletion(h)
	return h, false
}

// autoDownload waits for metadata (≤5 min) then activates the torrent under
// the queue cap.
func (e *Engine) autoDownload(h *Handle) {
	select {
	case <-h.T.GotInfo():
	case <-e.bgCtx.Done():
		return
	case <-h.removed:
		return
	case <-time.After(5 * time.Minute):
		return
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return
	}
	e.queueOrActivate(h)
}

// watchCompletion promotes the next queued torrent when this one completes.
// Deliberately UNCONDITIONAL — the legacy gated its only completion-promotion
// site behind a nil-Bloom check, stranding completed slots (§6). The Bloom
// auto-confirm hangs off this watcher in a later slice; it never gates it.
// No wall-clock timeout: multi-day downloads are routine, and the goroutine's
// lifetime is already bounded by engine Close and torrent removal.
func (e *Engine) watchCompletion(h *Handle) {
	select {
	case <-h.T.GotInfo():
	case <-e.bgCtx.Done():
		return
	case <-h.removed:
		return
	}
	select {
	case <-h.T.Complete().On():
	case <-e.bgCtx.Done():
		return
	case <-h.removed:
		return
	}
	// (Slice 5: bloom auto-confirm slots in here, behind a nil-check.)
	e.promoteQueued()
}
