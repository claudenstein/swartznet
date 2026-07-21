package gui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/daemon"
)

const statusPollInterval = 2 * time.Second

// statusTab renders live node status. Every value comes from a Daemon/engine
// accessor — the GUI computes no state of its own.
type statusTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon
	body    *widget.Label
}

func newStatusTab(ctx context.Context, d *daemon.Daemon) *statusTab {
	st := &statusTab{d: d}
	st.body = widget.NewLabel("")
	st.body.TextStyle.Monospace = true
	st.content = container.NewVScroll(container.NewPadded(st.body))
	st.refresh()
	go st.pollLoop(ctx)
	return st
}

func (st *statusTab) pollLoop(ctx context.Context) {
	t := time.NewTicker(statusPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Compute the (expensive) status OFF the Fyne UI thread — render()
			// calls indexer.Stats(), a full-corpus scan under the index lock that
			// can far exceed a frame budget on a real corpus. Running it inside
			// fyne.Do would block ALL rendering/input (the GLFW main loop drains
			// the func-queue and draws in mutually-exclusive cases), freezing the
			// whole GUI every 2s. Only the widget mutation goes on the UI thread.
			text := st.render()
			fyne.Do(func() { st.body.SetText(text) })
		}
	}
}

// refresh renders synchronously; used for the one-time initial paint during
// construction. The recurring poll computes render() off-thread (see pollLoop).
func (st *statusTab) refresh() {
	st.body.SetText(st.render())
}

func (st *statusTab) render() string {
	e := st.d.Eng
	if e == nil {
		return "engine unavailable"
	}
	var b []string
	add := func(f string, a ...any) { b = append(b, fmt.Sprintf(f, a...)) }

	// Torrents.
	snaps := e.TorrentSnapshots()
	var downloading, seeding, queued, paused int
	var dlRate, ulRate int64
	for _, s := range snaps {
		switch s.Status {
		case "downloading":
			downloading++
		case "seeding":
			seeding++
		case "queued":
			queued++
		case "paused":
			paused++
		}
		dlRate += s.DownloadRate
		ulRate += s.UploadRate
	}
	add("Torrents")
	add("  total=%d  downloading=%d  seeding=%d  queued=%d  paused=%d", len(snaps), downloading, seeding, queued, paused)
	add("  ↓ %s/s   ↑ %s/s", humanBytes(dlRate), humanBytes(ulRate))
	add("")

	// Local index (Layer L).
	add("Local Index")
	if idx := st.d.Idx; idx != nil {
		if s, err := idx.Stats(); err == nil {
			add("  documents=%d  torrents=%d  content=%d  disk=%s", s.DocCount, s.TorrentCount, s.ContentCount, humanBytes(s.DirBytes))
		} else {
			add("  (error: %v)", err)
		}
	} else {
		add("  (disabled)")
	}
	add("")

	// Swarm (Layer S).
	sw := e.SwarmSearch()
	add("Swarm Peers")
	add("  known=%d  search-capable=%d", sw.KnownPeers(), sw.CapablePeerCount())
	add("")

	// Capable-peer discovery (rendezvous + sn_peers PEX).
	rvSwarms := len(e.RendezvousInfoHashes())
	gossip := "no"
	if rvSwarms > 0 {
		gossip = "yes"
	}
	add("Discovery (rendezvous + PEX)")
	add("  rendezvous-swarms=%d  gossiping=%s", rvSwarms, gossip)
	add("")

	// DHT routing.
	good, total := e.DHTRoutingTableSize()
	add("DHT Routing")
	add("  good=%d  total=%d", good, total)
	add("")

	// Layer D publisher + reconciliation cache.
	ps := e.PublisherStatus()
	add("Layer D / Aggregate")
	add("  published keywords=%d  hits=%d", ps.TotalKeywords, ps.TotalHits)
	if rc := e.RecordCache(); rc != nil {
		add("  reconciliation cache=%d records", rc.Len())
	}
	add("  services mask=%s", ltepwire.FormatHex(e.ServicesMask()))
	add("")

	// Reputation (top 5).
	add("Reputation")
	if tr := e.ReputationTracker(); tr != nil {
		snap := tr.Snapshot()
		add("  known indexers=%d", len(snap))
		for i, r := range snap {
			if i >= 5 {
				break
			}
			add("  %s  score=%.3f  returned=%d confirmed=%d flagged=%d",
				short16(string(r.PubKey)), r.Score, r.Counters.HitsReturned, r.Counters.HitsConfirmed, r.Counters.HitsFlagged)
		}
	} else {
		add("  (disabled)")
	}

	return joinLines(b)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
