package gui

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/reputation"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

type searchTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon

	queryEntry *widget.Entry
	localChk   *widget.Check
	swarmChk   *widget.Check
	dhtChk     *widget.Check
	limitEntry *widget.Entry
	searchBtn  *widget.Button
	statusLbl  *widget.Label
	progress   *widget.ProgressBarInfinite

	resultBox *fyne.Container
	// emptyState is shown in resultBox before any search runs, or
	// when a search returns zero hits from every enabled layer.
	// It gives new users a starting point rather than an empty
	// panel.
	emptyState *fyne.Container
}

func newSearchTab(_ context.Context, d *daemon.Daemon) *searchTab {
	st := &searchTab{d: d}

	st.queryEntry = widget.NewEntry()
	st.queryEntry.SetPlaceHolder("Search query...")
	st.queryEntry.OnSubmitted = func(_ string) { st.runSearch() }

	st.localChk = widget.NewCheck("Local", nil)
	st.localChk.SetChecked(true)
	st.swarmChk = widget.NewCheck("Swarm", nil)
	st.dhtChk = widget.NewCheck("DHT", nil)

	st.limitEntry = widget.NewEntry()
	st.limitEntry.SetPlaceHolder("20")
	st.limitEntry.SetText("20")

	st.searchBtn = widget.NewButtonWithIcon("Search", theme.SearchIcon(), func() {
		st.runSearch()
	})

	st.statusLbl = widget.NewLabel("")
	st.statusLbl.TextStyle.Italic = true
	st.progress = widget.NewProgressBarInfinite()
	st.progress.Stop()
	st.progress.Hide()

	st.resultBox = container.NewVBox()

	// Empty-state panel shown inside the result area before any
	// search has run. Displays a short hint so the tab isn't just
	// blank canvas. Hidden once results start flowing in, re-shown
	// if every enabled layer returns zero hits.
	emptyTitle := widget.NewLabelWithStyle(
		"Search across your local index, the swarm, and the DHT",
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)
	emptyHint := widget.NewLabelWithStyle(
		"Type a query above and press Enter. Toggle Swarm / DHT\n"+
			"to broaden the search beyond your own index.",
		fyne.TextAlignCenter,
		fyne.TextStyle{},
	)
	st.emptyState = container.NewCenter(container.NewVBox(emptyTitle, emptyHint))

	optionsRow := container.NewHBox(
		st.localChk, st.swarmChk, st.dhtChk,
		widget.NewLabel("Limit:"), st.limitEntry,
	)

	queryRow := container.NewBorder(nil, nil, nil, st.searchBtn, st.queryEntry)
	header := container.NewVBox(queryRow, optionsRow, st.statusLbl, st.progress)

	// Stack the empty-state overlay on top of the result box; we
	// flip visibility in buildResults.
	body := container.NewStack(container.NewVScroll(st.resultBox), st.emptyState)
	st.content = container.NewBorder(header, nil, nil, nil, body)

	return st
}

// afterRunSearch, when non-nil, is invoked at the very end of the
// runSearch goroutine (after the fyne.Do callback that renders results
// returns). It exists only so tests can deterministically join the async
// UI goroutine under the Fyne test driver, which runs fyne.Do callbacks
// inline on the calling goroutine. It is nil in production, so real-app
// timing/behavior is unchanged.
var afterRunSearch func()

func (st *searchTab) runSearch() {
	q := strings.TrimSpace(st.queryEntry.Text)
	if q == "" {
		return
	}

	st.searchBtn.Disable()
	st.statusLbl.SetText("Searching...")
	st.progress.Show()
	st.progress.Start()
	st.resultBox.RemoveAll()
	// Hide the empty-state hint the moment a search fires; it
	// either gets replaced by result cards or re-shown below in
	// buildResults if every layer returned zero hits.
	st.emptyState.Hide()

	limit := 20
	if v := strings.TrimSpace(st.limitEntry.Text); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}

	doLocal := st.localChk.Checked
	doSwarm := st.swarmChk.Checked
	doDHT := st.dhtChk.Checked

	go func() {
		var wg sync.WaitGroup
		var (
			localResp *indexer.SearchResponse
			localErr  error
			swarmResp *swarmsearch.QueryResponse
			swarmErr  error
			dhtResp   *dhtindex.LookupResponse
			dhtErr    error
		)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if doLocal && st.d.Index != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := st.d.Index.Search(indexer.SearchRequest{
					Query: q,
					Limit: limit,
				})
				localResp = resp
				localErr = err
			}()
		}

		if doSwarm && st.d.Eng.SwarmSearch() != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := st.d.Eng.SwarmSearch().Query(ctx, swarmsearch.QueryRequest{
					Q:            q,
					PerPeerLimit: limit,
					Timeout:      2 * time.Second,
				})
				swarmResp = resp
				swarmErr = err
			}()
		}

		if doDHT && st.d.Eng.Lookup() != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := st.d.Eng.Lookup().Query(ctx, q)
				dhtResp = resp
				dhtErr = err
			}()
		}

		wg.Wait()

		fyne.Do(func() {
			st.searchBtn.Enable()
			st.progress.Stop()
			st.progress.Hide()
			st.buildResults(q, localResp, localErr, swarmResp, swarmErr, dhtResp, dhtErr)
		})
		// fyne.Do under the test driver runs its callback inline+synchronously,
		// so by here the async render has finished. Signal the test seam (nil in
		// production) so tests can join this goroutine deterministically.
		if afterRunSearch != nil {
			afterRunSearch()
		}
	}()
}

func (st *searchTab) buildResults(
	q string,
	localResp *indexer.SearchResponse, localErr error,
	swarmResp *swarmsearch.QueryResponse, swarmErr error,
	dhtResp *dhtindex.LookupResponse, dhtErr error,
) {
	st.resultBox.RemoveAll()

	var parts []string

	// Local results.
	if localResp != nil {
		parts = append(parts, fmt.Sprintf("Local: %d hits", localResp.Total))
		for _, h := range localResp.Hits {
			st.resultBox.Add(st.makeLocalHitCard(h))
		}
	} else if localErr != nil {
		parts = append(parts, fmt.Sprintf("Local: error: %v", localErr))
	}

	// Swarm results.
	if swarmResp != nil {
		parts = append(parts, fmt.Sprintf("Swarm: %d hits (asked=%d, responded=%d)", len(swarmResp.Hits), swarmResp.Asked, swarmResp.Responded))
		for _, h := range swarmResp.Hits {
			st.resultBox.Add(st.makeSwarmHitCard(h))
		}
	} else if swarmErr != nil {
		parts = append(parts, fmt.Sprintf("Swarm: error: %v", swarmErr))
	}

	// DHT results.
	if dhtResp != nil {
		parts = append(parts, fmt.Sprintf("DHT: %d hits (indexers=%d/%d)", len(dhtResp.Hits), dhtResp.IndexersResponded, dhtResp.IndexersAsked))
		for _, h := range dhtResp.Hits {
			st.resultBox.Add(st.makeDHTHitCard(h))
		}
	} else if dhtErr != nil {
		parts = append(parts, fmt.Sprintf("DHT: error: %v", dhtErr))
	}

	if len(parts) == 0 {
		parts = append(parts, "No search layers enabled")
	}
	st.statusLbl.SetText(strings.Join(parts, "  |  "))

	// If nothing rendered, swap in a "no results" hint so the
	// panel doesn't look broken. The empty-state widget changes
	// its text to reflect "no results for '<q>'" rather than the
	// initial generic hint, but we stuck with container.NewStack
	// so we can't replace children without re-laying out — so we
	// just show the original hint; users can tell they got no
	// hits from the "Local: 0 hits" in the status line above.
	if len(st.resultBox.Objects) == 0 {
		st.emptyState.Show()
	} else {
		st.emptyState.Hide()
	}
	st.resultBox.Refresh()
}

func (st *searchTab) makeLocalHitCard(h indexer.SearchHit) fyne.CanvasObject {
	title := h.Name
	if title == "" {
		title = h.InfoHash
	}
	subtitle := fmt.Sprintf("[%s] %s  score=%.2f", h.DocType, h.InfoHash[:16], h.Score)
	if h.DocType == "content" {
		subtitle += fmt.Sprintf("  file=%s", h.FilePath)
	}
	if h.SizeBytes > 0 {
		subtitle += "  " + humanBytes(h.SizeBytes)
	}
	if h.SignedBy != "" {
		// Indicate signature on the subtitle. Trusted-publisher
		// resolution would need an extra round-trip to the
		// engine's TrustStore — defer that to a future iteration
		// where SearchHit carries TrustedPublisher directly.
		subtitle += fmt.Sprintf("  ✓ signed by %s", h.SignedBy[:8])
	}

	confirmBtn := widget.NewButton("Confirm", func() {
		st.confirmHit(h.InfoHash)
	})
	flagBtn := widget.NewButton("Flag", func() {
		st.flagHit(h.InfoHash)
	})
	actions := container.NewHBox(confirmBtn, flagBtn)

	card := widget.NewCard(title, subtitle, actions)
	return st.wrapHitMenu(card, h.InfoHash, h.Name, h.SignedBy)
}

func (st *searchTab) makeSwarmHitCard(h swarmsearch.MergedHit) fyne.CanvasObject {
	title := h.Name
	if title == "" {
		title = h.InfoHash
	}
	subtitle := fmt.Sprintf("[swarm] %s  score=%d  seeders=%d  sources=%d",
		h.InfoHash[:16], h.Score, h.Seeders, len(h.Sources))
	if h.Size > 0 {
		subtitle += "  " + humanBytes(h.Size)
	}

	confirmBtn := widget.NewButton("Confirm", func() {
		st.confirmHit(h.InfoHash)
	})
	flagBtn := widget.NewButton("Flag", func() {
		st.flagHit(h.InfoHash)
	})

	card := widget.NewCard(title, subtitle, container.NewHBox(confirmBtn, flagBtn))
	return st.wrapHitMenu(card, h.InfoHash, h.Name, "")
}

func (st *searchTab) makeDHTHitCard(h dhtindex.LookupHit) fyne.CanvasObject {
	title := h.Name
	if title == "" {
		title = h.InfoHash
	}
	subtitle := fmt.Sprintf("[dht] %s  score=%.2f  seeders=%d  sources=%d",
		h.InfoHash[:16], h.Score, h.Seeders, len(h.Sources))
	if h.Size > 0 {
		subtitle += "  " + humanBytes(h.Size)
	}
	if h.BloomHit {
		subtitle += "  (known-good)"
	}

	confirmBtn := widget.NewButton("Confirm", func() {
		st.confirmHit(h.InfoHash)
	})
	flagBtn := widget.NewButton("Flag", func() {
		st.flagHit(h.InfoHash)
	})

	card := widget.NewCard(title, subtitle, container.NewHBox(confirmBtn, flagBtn))
	return st.wrapHitMenu(card, h.InfoHash, h.Name, "")
}

// wrapHitMenu wraps a search hit card so a right-click pops up
// the same actions a power user would otherwise have to chase
// across two buttons + the Downloads tab. The resulting menu
// covers the canonical "I see something interesting in the
// results — now what?" actions: add this torrent to the engine,
// copy the magnet/infohash for sharing, confirm or flag the
// reputation signal, and copy the publisher pubkey when one is
// attached.
func (st *searchTab) wrapHitMenu(card fyne.CanvasObject, infoHash, name, signedBy string) fyne.CanvasObject {
	build := func() *fyne.Menu {
		magnet := "magnet:?xt=urn:btih:" + infoHash
		if name != "" {
			magnet += "&dn=" + name
		}
		items := []*fyne.MenuItem{
			fyne.NewMenuItem("Add to downloads", func() {
				go func() {
					if _, err := st.d.Eng.AddMagnetURI(magnet); err != nil {
						fyne.Do(func() {
							dialog.ShowError(err, st.win())
						})
					}
				}()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Copy magnet link", func() {
				fyne.CurrentApp().Clipboard().SetContent(magnet)
			}),
			fyne.NewMenuItem("Copy infohash", func() {
				fyne.CurrentApp().Clipboard().SetContent(infoHash)
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Confirm (mark known-good)", func() { st.confirmHit(infoHash) }),
			fyne.NewMenuItem("Flag as spam", func() { st.flagHit(infoHash) }),
		}
		if signedBy != "" {
			signer := signedBy
			items = append(items,
				fyne.NewMenuItemSeparator(),
				fyne.NewMenuItem("Copy publisher pubkey", func() {
					fyne.CurrentApp().Clipboard().SetContent(signer)
				}),
			)
		}
		return fyne.NewMenu("Search hit", items...)
	}
	return newRightClickCapture(card, build)
}

func (st *searchTab) confirmHit(infoHashHex string) {
	bloom := st.d.Eng.KnownGoodBloom()
	if bloom == nil {
		return
	}
	ih, err := hex.DecodeString(infoHashHex)
	if err != nil || len(ih) != 20 {
		return
	}
	bloom.Add(ih)

	// Also record confirmed for source attribution.
	tracker := st.d.Eng.ReputationTracker()
	sources := st.d.Eng.SourceTracker()
	if tracker != nil && sources != nil {
		pks := sources.Sources(infoHashHex)
		if len(pks) > 0 {
			tracker.RecordConfirmed(pks...)
		}
	}

	dialog.ShowInformation("Confirmed", fmt.Sprintf("Marked %s as known-good", infoHashHex[:16]), st.win())
}

func (st *searchTab) flagHit(infoHashHex string) {
	tracker := st.d.Eng.ReputationTracker()
	if tracker == nil {
		return
	}

	var pks []reputation.PubKeyHex
	sources := st.d.Eng.SourceTracker()
	if sources != nil {
		pks = sources.Sources(infoHashHex)
	}
	if len(pks) == 0 {
		// Fallback: demote all known indexers.
		snap := tracker.Snapshot()
		pks = make([]reputation.PubKeyHex, 0, len(snap))
		for _, e := range snap {
			pks = append(pks, e.PubKey)
		}
	}

	tracker.RecordFlagged(pks...)
	if sources != nil {
		sources.Forget(infoHashHex)
	}

	dialog.ShowInformation("Flagged", fmt.Sprintf("Flagged %s as spam (%d indexers demoted)", infoHashHex[:16], len(pks)), st.win())
}

func (st *searchTab) win() fyne.Window { return windowForObject(st.content) }
