package engine

import "github.com/anacrolix/torrent"

// Queue management.
//
// anacrolix/torrent has no built-in concept of "max N active
// downloads at once"; every torrent added starts downloading
// immediately. SwartzNet layers a simple FIFO queue on top so
// users can cap concurrency (matching qBittorrent's "queue
// system" knob).
//
// Ordering:
//
//   Every handle gets a monotonic queueOrder at registration
//   time. Promotion iterates queued handles sorted ascending by
//   queueOrder — oldest goes first — so the behaviour is FIFO by
//   add time. Users can call QueueMoveToFront / QueueMoveToBack
//   to override the natural order: move-to-front sets
//   queueOrder to the minimum of all existing orders minus 1,
//   move-to-back sets it to the maximum plus 1.
//
// Rules:
//
//   - A torrent is "active" when it has any non-None file priority
//     AND is not paused AND is not fully complete.
//   - When maxActiveDownloads > 0 and the active count would
//     exceed the limit, newly-added torrents stay in "queued"
//     state: autoDownload does NOT flip their files to Normal
//     priority. A paused/completed/removed torrent releases its
//     slot and the oldest queued torrent is promoted.
//   - maxActiveDownloads == 0 disables the cap (unlimited, the
//     default and previous behaviour).
//   - Seeding-only torrents (complete) never occupy a download
//     slot.

// MaxActiveDownloads returns the current concurrent-download cap.
// Zero means unlimited.
func (e *Engine) MaxActiveDownloads() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxActiveDownloads
}

// SetMaxActiveDownloads sets the concurrent-download cap. Zero
// disables the cap. Reducing the cap does NOT pause already-
// active torrents; it only takes effect for newly-added torrents.
// Raising the cap immediately promotes queued torrents if any are
// waiting.
func (e *Engine) SetMaxActiveDownloads(n int) {
	if n < 0 {
		n = 0
	}
	e.mu.Lock()
	e.maxActiveDownloads = n
	e.mu.Unlock()
	e.log.Info("engine.max_active_downloads_set", "limit", n)
	e.promoteQueuedLocked()
}

// countActiveDownloadsLocked returns the number of currently-
// downloading (non-paused, non-complete, non-queued) handles.
// Caller must NOT hold e.mu.
func (e *Engine) countActiveDownloads() int {
	e.mu.Lock()
	handles := make([]*Handle, 0, len(e.handles))
	for _, h := range e.handles {
		handles = append(handles, h)
	}
	e.mu.Unlock()

	n := 0
	for _, h := range handles {
		if h.IsPaused() || h.IsQueued() {
			continue
		}
		if t := h.T; t != nil {
			if info := t.Info(); info != nil {
				if t.BytesMissing() <= 0 {
					// seeding, doesn't count against the download limit
					continue
				}
			}
			// treat metadata-only torrents as active (they are
			// consuming download attention)
		}
		n++
	}
	return n
}

// queueOrActivateLocked decides whether a newly-added handle
// should start downloading immediately or sit in the queue.
// Callers who just finished creating a Handle can invoke this
// instead of calling autoDownload's SetPriority path directly.
func (e *Engine) queueOrActivate(h *Handle) {
	e.mu.Lock()
	cap := e.maxActiveDownloads
	e.mu.Unlock()

	if cap == 0 {
		// unlimited — activate immediately
		activateDownload(h)
		return
	}

	active := e.countActiveDownloads()
	if active < cap {
		activateDownload(h)
		return
	}

	// Over cap — keep in queued state. The handle's indexing
	// goroutine still runs (metadata arrives normally); only
	// the file-priority flip is deferred.
	h.setQueued(true)
	e.log.Info("engine.torrent_queued", "info_hash", h.T.InfoHash().HexString())
}

// activateDownload flips every file in a handle's torrent to
// Normal priority, matching autoDownload's default. Called from
// queueOrActivate and from promoteQueued.
func activateDownload(h *Handle) {
	h.setQueued(false)
	if h.T.Info() == nil {
		// Metadata not here yet; autoDownload goroutine will
		// flip priorities once GotInfo fires.
		return
	}
	for _, f := range h.T.Files() {
		f.SetPriority(torrent.PiecePriorityNormal)
	}
}

// promoteQueuedLocked examines the handles map and promotes
// queued torrents while under the active-downloads cap. Called
// whenever a slot might have opened up (pause, complete, remove,
// cap raised).
func (e *Engine) promoteQueuedLocked() {
	e.mu.Lock()
	cap := e.maxActiveDownloads
	handles := make([]*Handle, 0, len(e.handles))
	for _, h := range e.handles {
		handles = append(handles, h)
	}
	e.mu.Unlock()

	if cap == 0 {
		// Unlimited: promote everything queued.
		for _, h := range handles {
			if h.IsQueued() {
				activateDownload(h)
			}
		}
		return
	}

	active := e.countActiveDownloads()
	// Sort queued handles by queueOrder ascending (oldest first)
	// so promotion is FIFO by add time unless the user has
	// called QueueMoveToFront/Back.
	sortHandlesByQueueOrder(handles)
	for _, h := range handles {
		if active >= cap {
			break
		}
		if h.IsQueued() {
			activateDownload(h)
			active++
		}
	}
}

// QueueMoveToFront reassigns the given handle's queueOrder so it
// will promote before any other currently-tracked handle. Only
// affects ordering: the handle still respects the download cap
// and must be Queued for the move to have any visible effect.
// Idempotent.
func (e *Engine) QueueMoveToFront(infoHashHex string) error {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return err
	}

	e.mu.Lock()
	// Find the smallest queueOrder in the map.
	var minOrder int64
	first := true
	for _, other := range e.handles {
		other.queueMu.Lock()
		o := other.queueOrder
		other.queueMu.Unlock()
		if first || o < minOrder {
			minOrder = o
			first = false
		}
	}
	e.mu.Unlock()

	h.queueMu.Lock()
	h.queueOrder = minOrder - 1
	h.queueMu.Unlock()

	e.log.Info("engine.queue_move_to_front", "info_hash", infoHashHex)
	// Promotion may want to start this one now.
	go e.promoteQueuedLocked()
	return nil
}

// QueueMoveToBack is the mirror of QueueMoveToFront: the handle
// will promote last among all tracked handles.
func (e *Engine) QueueMoveToBack(infoHashHex string) error {
	h, err := e.handleByHex(infoHashHex)
	if err != nil {
		return err
	}

	e.mu.Lock()
	var maxOrder int64
	first := true
	for _, other := range e.handles {
		other.queueMu.Lock()
		o := other.queueOrder
		other.queueMu.Unlock()
		if first || o > maxOrder {
			maxOrder = o
			first = false
		}
	}
	e.mu.Unlock()

	h.queueMu.Lock()
	h.queueOrder = maxOrder + 1
	h.queueMu.Unlock()

	e.log.Info("engine.queue_move_to_back", "info_hash", infoHashHex)
	return nil
}

// sortHandlesByQueueOrder sorts handles ascending by their
// queueOrder field. Stable w.r.t. equal orders (simple bubble
// sort; the slice rarely exceeds a handful of handles).
func sortHandlesByQueueOrder(handles []*Handle) {
	for i := 0; i < len(handles); i++ {
		for j := i + 1; j < len(handles); j++ {
			handles[i].queueMu.Lock()
			a := handles[i].queueOrder
			handles[i].queueMu.Unlock()
			handles[j].queueMu.Lock()
			b := handles[j].queueOrder
			handles[j].queueMu.Unlock()
			if a > b {
				handles[i], handles[j] = handles[j], handles[i]
			}
		}
	}
}
