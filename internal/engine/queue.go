package engine

import (
	"sort"

	"github.com/anacrolix/torrent"
)

// MaxActiveDownloads reports the queue cap (0 = unlimited).
func (e *Engine) MaxActiveDownloads() int {
	e.promoteMu.Lock()
	defer e.promoteMu.Unlock()
	return e.maxActiveDownloads
}

// SetMaxActiveDownloads sets the cap (negatives clamp to 0 = unlimited) and
// runs a promotion pass. Reducing the cap never demotes active torrents.
func (e *Engine) SetMaxActiveDownloads(n int) {
	if n < 0 {
		n = 0
	}
	e.promoteMu.Lock()
	e.maxActiveDownloads = n
	e.promoteMu.Unlock()
	e.log.Info("engine.max_active_downloads_set", "limit", n)
	e.promoteQueued()
}

// isActive implements the documented slot rule FOR REAL (the legacy never
// inspected file priorities — §6): a torrent is active iff it has any
// non-None file priority AND is not paused AND is not queued AND is not
// complete. Metadata-only torrents count as active (they consume download
// attention); seeding torrents never occupy a slot.
func (h *Handle) isActive() bool {
	if h.isPaused() || h.isQueued() {
		return false
	}
	info := h.T.Info()
	if info == nil {
		return true
	}
	if h.T.BytesMissing() <= 0 {
		return false
	}
	for _, f := range h.T.Files() {
		if f.Priority() != torrent.PiecePriorityNone {
			return true
		}
	}
	return false
}

// countActive counts slot-occupying torrents. Caller holds promoteMu.
func (e *Engine) countActive() int {
	n := 0
	for _, h := range e.Torrents() {
		if h.isActive() {
			n++
		}
	}
	return n
}

// queueOrActivate activates h under the cap or queues it. Paused handles are
// untouched (Resume re-runs activation). Serialized on promoteMu so
// concurrent autoDownload goroutines from a batch restore cannot
// over-subscribe the cap. (Honest name: this acquires its own locks —
// callers must NOT hold e.mu.)
func (e *Engine) queueOrActivate(h *Handle) {
	if h.isPaused() {
		return
	}
	e.promoteMu.Lock()
	defer e.promoteMu.Unlock()
	if e.maxActiveDownloads == 0 || e.countActive() < e.maxActiveDownloads {
		e.activateDownload(h)
		return
	}
	h.setQueued(true)
	// A previously-activated handle (e.g. resumed over a full cap) must not
	// keep downloading from the queue: reset its priorities — promotion
	// re-flips them. Without this, a resume-over-cap torrent transfers
	// outside the cap while countActive no longer counts it.
	if h.T.Info() != nil {
		for _, f := range h.T.Files() {
			f.SetPriority(torrent.PiecePriorityNone)
		}
	}
	e.log.Info("engine.torrent_queued", "info_hash", h.InfoHashHex())
}

// activateDownload flips every file to Normal priority — NEVER DownloadAll:
// anacrolix keeps two priority surfaces and DownloadAll leaves File.Priority
// stuck at "none" in snapshots. Paused handles are refused with queued state
// untouched so a later Resume can promote.
func (e *Engine) activateDownload(h *Handle) {
	if h.isPaused() {
		return
	}
	h.setQueued(false)
	if h.T.Info() == nil {
		return // autoDownload flips after metadata arrives
	}
	for _, f := range h.T.Files() {
		f.SetPriority(torrent.PiecePriorityNormal)
	}
}

// promoteQueued promotes queued torrents FIFO by queue order while slots are
// free. Called on cap change, pause, remove, completion (unconditionally —
// the §6 fix), and move-to-front.
func (e *Engine) promoteQueued() {
	e.promoteMu.Lock()
	defer e.promoteMu.Unlock()
	handles := e.Torrents()
	sort.Slice(handles, func(i, j int) bool {
		return handles[i].getQueueOrder() < handles[j].getQueueOrder()
	})
	if e.maxActiveDownloads == 0 {
		for _, h := range handles {
			if h.isQueued() {
				e.activateDownload(h)
			}
		}
		return
	}
	active := e.countActive()
	for _, h := range handles {
		if active >= e.maxActiveDownloads {
			return
		}
		if h.isQueued() && !h.isPaused() {
			e.activateDownload(h)
			active++
		}
	}
}

// QueueMoveToFront makes h next in line and runs a promotion pass. Queue
// moves are runtime-only (legacy parity): the order is not persisted.
func (e *Engine) QueueMoveToFront(ihHex string) error {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return err
	}
	min := h.getQueueOrder()
	for _, other := range e.Torrents() {
		if qo := other.getQueueOrder(); qo < min {
			min = qo
		}
	}
	h.setQueueOrder(min - 1)
	e.log.Info("engine.queue_move_to_front", "info_hash", h.InfoHashHex())
	go e.promoteQueued()
	return nil
}

// QueueMoveToBack demotes h to the end. Deliberately fires no promotion pass
// — moving back cannot free a slot.
func (e *Engine) QueueMoveToBack(ihHex string) error {
	h, err := e.handleByHex(ihHex)
	if err != nil {
		return err
	}
	max := h.getQueueOrder()
	for _, other := range e.Torrents() {
		if qo := other.getQueueOrder(); qo > max {
			max = qo
		}
	}
	h.setQueueOrder(max + 1)
	e.log.Info("engine.queue_move_to_back", "info_hash", h.InfoHashHex())
	return nil
}
