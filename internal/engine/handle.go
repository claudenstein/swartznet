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
	companion  bool // a companion-index bookkeeping torrent: never indexed, minted, or Layer-D published
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

func (h *Handle) setSignedBy(v string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.signedBy = v
}

// setSignedByIfEmpty is the compare-and-set behind the sticky upgrade: only
// the first non-empty attribution wins, so concurrent signed adds cannot
// leave the handle and session disagreeing.
func (h *Handle) setSignedByIfEmpty(v string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.signedBy != "" {
		return false
	}
	h.signedBy = v
	return true
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

func (h *Handle) setIndexing(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.indexing = v
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
// side effects. All persisted state (paused, indexing, signedBy, queueOrder)
// is applied to the handle BEFORE any goroutine spawns, so a restored torrent
// can never race its index/download goroutines with the restore overrides —
// pass restore for a session restore, nil for a fresh add.
func (e *Engine) registerLocked(t *torrent.Torrent, paused bool) (h *Handle, existed bool) {
	return e.registerLockedRestore(t, paused, nil, false)
}

// registerLockedCompanion registers a companion-index bookkeeping torrent: it
// is seeded/fetched like any torrent but is NEVER indexed, minted, or Layer-D
// published (autoIndex early-returns on the companion flag), so a node's own
// companion torrents cannot pollute its published corpus or leak
// "swartznet-content-index-*" filenames onto the public DHT keyword index.
func (e *Engine) registerLockedCompanion(t *torrent.Torrent) (h *Handle, existed bool) {
	return e.registerLockedRestore(t, false, nil, true)
}

func (e *Engine) registerLockedRestore(t *torrent.Torrent, paused bool, restore *sessionEntry, companion bool) (h *Handle, existed bool) {
	if h, ok := e.handles[t.InfoHash()]; ok {
		return h, true
	}
	h = &Handle{
		T:         t,
		eng:       e,
		paused:    paused,
		indexing:  !companion,
		companion: companion,
		removed:   make(chan struct{}),
		pieceSub:  startPieceSubscription(t, e.log),
		fileSub:   startFileTracker(t, e.bgCtx, e.log),
	}
	if restore != nil {
		h.indexing = restore.Indexing
		h.signedBy = restore.SignedBy
		h.queueOrder = restore.QueueOrder
		if e.nextQueueOrder < restore.QueueOrder {
			e.nextQueueOrder = restore.QueueOrder
		}
	} else {
		e.nextQueueOrder++
		h.queueOrder = e.nextQueueOrder
	}
	e.handles[t.InfoHash()] = h
	go e.autoDownload(h)
	go e.watchCompletion(h)
	go e.autoIndex(h)
	go e.ingestFileEvents(h)
	return h, false
}

// autoDownload waits for metadata then activates the torrent under the queue
// cap. It waits WITHOUT a wall-clock cap — the goroutine's lifetime is already
// bounded by engine close (bgCtx) and torrent removal (h.removed). A prior 5-min
// timeout abandoned activation, so a magnet whose metadata resolved later (a
// poorly-seeded infohash) was left permanently un-downloadable (priorities stuck
// at None) with no automatic recovery.
func (e *Engine) autoDownload(h *Handle) {
	select {
	case <-h.T.GotInfo():
	case <-e.bgCtx.Done():
		return
	case <-h.removed:
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
	// Promote the next queued torrent FIRST: freeing the slot is the
	// latency-sensitive act, and it must never wait on the Bloom side effect
	// below — not on its nil check and not on the synchronous Checkpoint's
	// disk I/O (the §6 stranded-slot fix means promotion is unconditional AND
	// unblocked). A crash between here and the Checkpoint is harmless: the
	// torrent is still complete on restart, so this watcher re-fires and
	// re-adds it to the Bloom idempotently.
	e.promoteQueued()

	// Companion bookkeeping torrents are NOT content — they must never enter the
	// known-good Bloom (which feeds Layer-D spam scoring); each companion refresh
	// would otherwise monotonically fill the fixed-size filter and erode the spam
	// signal. Promotion above stays unconditional (§6); only this side-effect is
	// gated, mirroring autoIndex's companion guard.
	if h.companion {
		return
	}
	// Auto-confirm the completed infohash into the known-good Bloom (a
	// benign self-signal — the node fully downloaded this content). This is
	// bloom.Add ONLY, never RecordConfirmed: completion must not
	// self-reinforce reputation (D22).
	if bloom := e.KnownGoodBloom(); bloom != nil {
		ih := h.T.InfoHash()
		bloom.Add(ih[:])
		e.log.Info("engine.bloom.auto_confirmed", "info_hash", h.InfoHashHex(), "name", h.T.Name())
		e.Checkpoint()
	}
}
