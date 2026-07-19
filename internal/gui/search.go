package gui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/searchmux"
)

// searchTab renders the three-layer search. It NEVER fans out itself: it issues
// one Daemon.Search and renders the shared searchmux.Result's per-layer L/S/D
// cards. Confirm/Flag on a card route through the daemon's shared spam path.
type searchTab struct {
	content    fyne.CanvasObject
	d          *daemon.Daemon
	queryEntry *widget.Entry
	swarmChk   *widget.Check
	dhtChk     *widget.Check
	limitEntry *widget.Entry
	searchBtn  *widget.Button
	statusLbl  *widget.Label
	progress   *widget.ProgressBarInfinite
	resultBox  *fyne.Container

	// afterSearch is a test seam invoked after results are rendered (nil in prod).
	afterSearch func()
}

func newSearchTab(_ context.Context, d *daemon.Daemon) *searchTab {
	st := &searchTab{d: d}
	st.queryEntry = widget.NewEntry()
	st.queryEntry.SetPlaceHolder("Search query...")
	st.queryEntry.OnSubmitted = func(string) { st.runSearch() }
	st.searchBtn = widget.NewButtonWithIcon("Search", theme.SearchIcon(), st.runSearch)
	st.swarmChk = widget.NewCheck("Swarm", nil)
	st.dhtChk = widget.NewCheck("DHT", nil)
	st.limitEntry = widget.NewEntry()
	st.limitEntry.SetPlaceHolder("20")
	st.limitEntry.SetText("20")
	st.statusLbl = widget.NewLabel("")
	st.statusLbl.TextStyle.Italic = true
	st.progress = widget.NewProgressBarInfinite()
	st.progress.Stop()
	st.progress.Hide()
	st.resultBox = container.NewVBox()

	queryRow := container.NewBorder(nil, nil, nil, st.searchBtn, st.queryEntry)
	optionsRow := container.NewHBox(
		widget.NewLabel("Layers:"), widget.NewLabel("Local (always)"),
		st.swarmChk, st.dhtChk, widget.NewLabel("Limit:"), st.limitEntry,
	)
	header := container.NewVBox(queryRow, optionsRow, st.statusLbl, st.progress)
	st.content = container.NewBorder(header, nil, nil, nil, container.NewVScroll(st.resultBox))
	return st
}

func parseSearchLimit(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 20
	}
	n, err := strconv.Atoi(text)
	if err != nil || n <= 0 {
		return 20
	}
	return n
}

func (st *searchTab) runSearch() {
	q := strings.TrimSpace(st.queryEntry.Text)
	if q == "" {
		return
	}
	limit := parseSearchLimit(st.limitEntry.Text)
	doSwarm, doDHT := st.swarmChk.Checked, st.dhtChk.Checked
	st.searchBtn.Disable()
	st.statusLbl.SetText("Searching...")
	st.progress.Show()
	st.progress.Start()
	st.resultBox.RemoveAll()

	go func() {
		// One deadline bounds the network layers; Daemon.Search fans out through
		// the SAME mux the web/CLI use — no GUI-private reconciliation.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res := st.d.Search(ctx, searchmux.Query{
			Text: q, Limit: limit, Swarm: doSwarm, DHT: doDHT, Highlight: true,
		})
		fyne.Do(func() {
			st.searchBtn.Enable()
			st.progress.Stop()
			st.progress.Hide()
			st.buildResults(res)
			if st.afterSearch != nil {
				st.afterSearch()
			}
		})
	}()
}

// buildResults renders each layer's native response as its own set of cards and
// a per-layer status line. Layers are never merged.
func (st *searchTab) buildResults(res searchmux.Result) {
	st.resultBox.RemoveAll()
	var parts []string

	// Layer L.
	switch {
	case res.LocalErr != nil:
		parts = append(parts, fmt.Sprintf("Local: error: %v", res.LocalErr))
	case res.Local != nil:
		parts = append(parts, fmt.Sprintf("Local: %d hits", res.Local.Total))
		for _, h := range res.Local.Hits {
			sub := fmt.Sprintf("[%s] %s  score=%.2f", h.DocType, short16(h.InfoHash), h.Score)
			if h.DocType == "content" && h.FilePath != "" {
				sub += "  file=" + h.FilePath
			}
			if h.SizeBytes > 0 {
				sub += "  " + humanBytes(h.SizeBytes)
			}
			if h.SignedBy != "" {
				sub += "  ✓ signed by " + short8(h.SignedBy)
			}
			st.addHitCard(h.Name, h.InfoHash, sub)
		}
	}

	// Layer S.
	if st.swarmChk.Checked {
		switch {
		case res.SwarmErr != nil:
			parts = append(parts, fmt.Sprintf("Swarm: error: %v", res.SwarmErr))
		case res.Swarm != nil:
			parts = append(parts, fmt.Sprintf("Swarm: %d hits (asked=%d, responded=%d)",
				len(res.Swarm.Hits), res.Swarm.Asked, res.Swarm.Responded))
			for _, h := range res.Swarm.Hits {
				sub := fmt.Sprintf("[swarm] %s  score=%d  seeders=%d  sources=%d",
					short16(h.InfoHash), h.Score, h.Seeders, len(h.Sources))
				st.addHitCard(h.Name, h.InfoHash, sub)
			}
		}
	}

	// Layer D.
	if st.dhtChk.Checked {
		switch {
		case res.DHTErr != nil:
			parts = append(parts, fmt.Sprintf("DHT: error: %v", res.DHTErr))
		case res.DHT != nil:
			parts = append(parts, fmt.Sprintf("DHT: %d hits (indexers=%d/%d)",
				len(res.DHT.Hits), res.DHT.IndexersResponded, res.DHT.IndexersAsked))
			for _, h := range res.DHT.Hits {
				sub := fmt.Sprintf("[dht] %s  score=%.2f  seeders=%d  sources=%d",
					short16(h.InfoHash), h.Score, h.Seeders, len(h.Sources))
				st.addHitCard(h.Name, h.InfoHash, sub)
			}
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "No search layers enabled")
	}
	st.statusLbl.SetText(strings.Join(parts, "  |  "))
	if len(st.resultBox.Objects) == 0 {
		st.resultBox.Add(widget.NewLabel("(no results)"))
	}
	st.resultBox.Refresh()
}

// addHitCard renders one hit with Confirm/Flag actions that route through the
// daemon's SHARED spam path (identical to the web UI / CLI).
func (st *searchTab) addHitCard(name, infohash, subtitle string) {
	title := name
	if title == "" {
		title = infohash
	}
	ih := infohash
	confirmBtn := widget.NewButton("Confirm", func() { st.confirm(ih) })
	flagBtn := widget.NewButton("Flag", func() { st.flag(ih) })
	card := widget.NewCard(title, subtitle, container.NewHBox(confirmBtn, flagBtn))
	st.resultBox.Add(card)
}

func (st *searchTab) confirm(ih string) {
	go func() {
		res, err := st.d.Confirm(ih)
		msg := fmt.Sprintf("confirmed %s", short16(ih))
		if err != nil {
			msg = "confirm failed: " + err.Error()
		} else if res.IndexersConfirmed > 0 {
			msg = fmt.Sprintf("confirmed %s (boosted %d indexer(s))", short16(ih), res.IndexersConfirmed)
		}
		fyne.Do(func() { st.statusLbl.SetText(msg) })
	}()
}

func (st *searchTab) flag(ih string) {
	go func() {
		res, err := st.d.Flag(ih)
		var msg string
		switch {
		case err != nil:
			msg = "flag failed: " + err.Error()
		case res.IndexersFlagged > 0:
			msg = fmt.Sprintf("flagged %s (demoted %d indexer(s))", short16(ih), res.IndexersFlagged)
		default:
			msg = fmt.Sprintf("flagged %s (no reputations changed: %s)", short16(ih), res.Attribution)
		}
		fyne.Do(func() { st.statusLbl.SetText(msg) })
	}()
}
