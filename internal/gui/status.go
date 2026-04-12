package gui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

type statusTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon

	// Card labels (updated by polling goroutine).
	indexCard   *widget.Card
	indexLabels []*widget.Label
	swarmCard   *widget.Card
	swarmLabels []*widget.Label
	pubCard     *widget.Card
	pubLabels   []*widget.Label
	bloomCard   *widget.Card
	bloomLabels []*widget.Label
	repCard     *widget.Card
	repList     *widget.List

	// Reputation snapshot for the list widget.
	repSnap []repRow
}

type repRow struct {
	pubkey string
	score  string
	hits   string
}

func newStatusTab(ctx context.Context, d *daemon.Daemon) *statusTab {
	st := &statusTab{d: d}

	// Index card.
	st.indexLabels = makeLabelGroup(4)
	st.indexCard = widget.NewCard("Local Index", "",
		container.NewVBox(
			labelRow("Documents:", st.indexLabels[0]),
			labelRow("Torrents:", st.indexLabels[1]),
			labelRow("Content docs:", st.indexLabels[2]),
			labelRow("Disk size:", st.indexLabels[3]),
		),
	)

	// Swarm card.
	st.swarmLabels = makeLabelGroup(2)
	st.swarmCard = widget.NewCard("Swarm Peers", "",
		container.NewVBox(
			labelRow("Known peers:", st.swarmLabels[0]),
			labelRow("Search-capable:", st.swarmLabels[1]),
		),
	)

	// Publisher card.
	st.pubLabels = makeLabelGroup(3)
	st.pubCard = widget.NewCard("DHT Publisher", "",
		container.NewVBox(
			labelRow("Keywords:", st.pubLabels[0]),
			labelRow("Total hits:", st.pubLabels[1]),
			labelRow("Pubkey:", st.pubLabels[2]),
		),
	)

	// Bloom card.
	st.bloomLabels = makeLabelGroup(1)
	st.bloomCard = widget.NewCard("Known-Good Filter", "",
		container.NewVBox(
			labelRow("Estimated items:", st.bloomLabels[0]),
		),
	)

	// Reputation card.
	st.repList = widget.NewList(
		func() int { return len(st.repSnap) },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewLabel("pubkey"),
				widget.NewLabel("score"),
				widget.NewLabel("hits"),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			box := obj.(*fyne.Container)
			if id >= len(st.repSnap) {
				return
			}
			r := st.repSnap[id]
			box.Objects[0].(*widget.Label).SetText(r.pubkey)
			box.Objects[1].(*widget.Label).SetText(r.score)
			box.Objects[2].(*widget.Label).SetText(r.hits)
		},
	)
	st.repCard = widget.NewCard("Reputation", "", st.repList)

	grid := container.NewAdaptiveGrid(2,
		st.indexCard,
		st.swarmCard,
		st.pubCard,
		st.bloomCard,
	)

	st.content = container.NewBorder(grid, nil, nil, nil, st.repCard)

	go st.pollLoop(ctx)

	return st
}

func (st *statusTab) pollLoop(ctx context.Context) {
	// Initial fetch.
	st.refresh()

	tick := time.NewTicker(4 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			st.refresh()
		}
	}
}

func (st *statusTab) refresh() {
	// Index stats.
	var docCount, torrentCount, contentCount uint64
	var dirBytes int64
	if st.d.Index != nil {
		if stats, err := st.d.Index.Stats(); err == nil {
			docCount = stats.DocCount
			torrentCount = stats.TorrentCount
			contentCount = stats.ContentCount
			dirBytes = stats.DirBytes
		}
	}

	// Swarm peers.
	var knownPeers, capablePeers int
	if sw := st.d.Eng.SwarmSearch(); sw != nil {
		peers := sw.KnownPeers()
		knownPeers = len(peers)
		for _, p := range peers {
			if p.Supported {
				capablePeers++
			}
		}
	}

	// Publisher.
	var pubKeywords, pubHits int
	var pubKey string
	if pub := st.d.Eng.Publisher(); pub != nil {
		ps := pub.Status()
		pubKeywords = ps.TotalKeywords
		pubHits = ps.TotalHits
	}
	if id := st.d.Eng.Identity(); id != nil {
		pubKey = id.PublicKeyHex()
		if len(pubKey) > 16 {
			pubKey = pubKey[:16] + "..."
		}
	}

	// Bloom.
	var bloomItems float64
	if bloom := st.d.Eng.KnownGoodBloom(); bloom != nil {
		bloomItems = bloom.EstimatedItems()
	}

	// Reputation.
	var rows []repRow
	if tracker := st.d.Eng.ReputationTracker(); tracker != nil {
		snap := tracker.Snapshot()
		for _, e := range snap {
			pk := string(e.PubKey)
			if len(pk) > 16 {
				pk = pk[:16] + "..."
			}
			rows = append(rows, repRow{
				pubkey: pk,
				score:  fmt.Sprintf("%.3f", e.Score),
				hits:   fmt.Sprintf("%d/%d/%d", e.Counters.HitsReturned, e.Counters.HitsConfirmed, e.Counters.HitsFlagged),
			})
		}
	}

	fyne.Do(func() {
		st.indexLabels[0].SetText(fmt.Sprintf("%d", docCount))
		st.indexLabels[1].SetText(fmt.Sprintf("%d", torrentCount))
		st.indexLabels[2].SetText(fmt.Sprintf("%d", contentCount))
		st.indexLabels[3].SetText(humanBytes(dirBytes))

		st.swarmLabels[0].SetText(fmt.Sprintf("%d", knownPeers))
		st.swarmLabels[1].SetText(fmt.Sprintf("%d", capablePeers))

		st.pubLabels[0].SetText(fmt.Sprintf("%d", pubKeywords))
		st.pubLabels[1].SetText(fmt.Sprintf("%d", pubHits))
		st.pubLabels[2].SetText(pubKey)

		st.bloomLabels[0].SetText(fmt.Sprintf("%.0f", bloomItems))

		st.repSnap = rows
		st.repList.Refresh()
	})
}

// labelRow creates a horizontal pair of label + value.
func labelRow(name string, value *widget.Label) fyne.CanvasObject {
	lbl := widget.NewLabel(name)
	lbl.TextStyle.Bold = true
	return container.NewHBox(lbl, value)
}

func makeLabelGroup(n int) []*widget.Label {
	out := make([]*widget.Label, n)
	for i := range out {
		out[i] = widget.NewLabel("-")
	}
	return out
}
