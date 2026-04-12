package engine

import (
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Queue management.
//
// anacrolix/torrent has no built-in concept of "max N active
// downloads at once"; every torrent added starts downloading
// immediately. SwartzNet layers a simple FIFO queue on top so
// users can cap concurrency (matching qBittorrent's "queue
// system" knob).
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
	// Iterate over handles by add order — map iteration is
	// random but we reorder by InfoHash for stable behaviour.
	// (InfoHash is not strictly add-order but it's
	// deterministic; this is a simple-queue-not-a-scheduler
	// feature, precise ordering is future work.)
	sortHandlesByAddOrder(handles)
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

// sortHandlesByAddOrder sorts handles in a stable way. We don't
// yet track add timestamps, so fall back to infohash bytes.
func sortHandlesByAddOrder(handles []*Handle) {
	// Bubble sort is fine for the tiny slices we expect (rarely
	// more than 20 torrents at a time) and avoids pulling in
	// sort.Slice for a tiny helper.
	for i := 0; i < len(handles); i++ {
		for j := i + 1; j < len(handles); j++ {
			if compareInfoHash(handles[i].T.InfoHash(), handles[j].T.InfoHash()) > 0 {
				handles[i], handles[j] = handles[j], handles[i]
			}
		}
	}
}

func compareInfoHash(a, b metainfo.Hash) int {
	for i := 0; i < 20; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
