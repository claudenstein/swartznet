package gui

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

type companionTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon

	// Publisher section.
	pubKeyLbl      *widget.Label
	pubRefreshLbl  *widget.Label
	pubCountLbl    *widget.Label
	pubErrorLbl    *widget.Label
	pubInfoHashLbl *widget.Label

	// Follow list.
	followList   *widget.List
	follows      []followRow
	followsEmpty *widget.Label // hint shown when the follow list is empty
	// followSelectedKey is the pubkey hex of the row the user last
	// clicked/right-clicked, NOT a row index. Rows are re-sorted on
	// every 4s refresh, so a stored index would point at a different
	// publisher after a refresh — keying by pubkey keeps the
	// context-menu (Unfollow / Copy) bound to the publisher the user
	// actually clicked. Empty string = nothing selected.
	followSelectedKey string
}

type followRow struct {
	pubkey   string
	label    string
	torrents int
	content  int
	lastSync string
	lastErr  string
}

func newCompanionTab(ctx context.Context, d *daemon.Daemon) *companionTab {
	ct := buildCompanionTab(d)
	go ct.pollLoop(ctx)
	return ct
}

// buildCompanionTab constructs the companionTab struct without
// spawning the pollLoop goroutine. Tests use it to avoid the
// pollLoop's fyne.Do refresh racing test-thread widget reads
// under -race.
func buildCompanionTab(d *daemon.Daemon) *companionTab {
	ct := &companionTab{d: d}

	// Publisher status labels.
	// pubKeyLbl shows the full 64-char ed25519 pubkey hex —
	// truncating it (the previous behaviour) made it useless for
	// the user-facing flow of "tell my friend my pubkey so they
	// can follow me", since the truncated form can't be pasted
	// back into the Follow form. Selectable=true lets the user
	// drag-select to copy, and the Copy button next to it does the
	// same in one click.
	ct.pubKeyLbl = widget.NewLabel("-")
	ct.pubKeyLbl.Wrapping = fyne.TextWrapBreak
	ct.pubKeyLbl.Selectable = true
	ct.pubKeyLbl.TextStyle = fyne.TextStyle{Monospace: true}
	ct.pubRefreshLbl = widget.NewLabel("-")
	ct.pubCountLbl = widget.NewLabel("-")
	ct.pubInfoHashLbl = widget.NewLabel("-")
	ct.pubErrorLbl = widget.NewLabel("")

	pubKeyCopyBtn := widget.NewButtonWithIcon("Copy", theme.ContentCopyIcon(), func() {
		v := ct.pubKeyLbl.Text
		if v == "" || v == "-" {
			return
		}
		fyne.CurrentApp().Clipboard().SetContent(v)
	})
	pubKeyCopyBtn.Importance = widget.LowImportance

	refreshBtn := widget.NewButton("Refresh Now", func() {
		ct.refreshPublisher()
	})

	pubKeyRow := container.NewBorder(nil, nil,
		boldLabel("Public Key:"), pubKeyCopyBtn,
		ct.pubKeyLbl,
	)

	pubCard := widget.NewCard("Companion Publisher", "", container.NewVBox(
		pubKeyRow,
		labelRow("Last Refresh:", ct.pubRefreshLbl),
		labelRow("Published:", ct.pubCountLbl),
		labelRow("Last InfoHash:", ct.pubInfoHashLbl),
		ct.pubErrorLbl,
		refreshBtn,
	))

	// Follow list.
	ct.followList = widget.NewList(
		func() int { return len(ct.follows) },
		func() fyne.CanvasObject {
			return container.NewVBox(
				widget.NewLabel("label (pubkey)"),
				widget.NewLabel("stats"),
				widget.NewButton("Unfollow", nil),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			box := obj.(*fyne.Container)
			if id >= len(ct.follows) {
				return
			}
			f := ct.follows[id]
			shortPK := f.pubkey
			if len(shortPK) > 16 {
				shortPK = shortPK[:16] + "..."
			}
			box.Objects[0].(*widget.Label).SetText(fmt.Sprintf("%s (%s)", f.label, shortPK))
			stats := fmt.Sprintf("torrents=%d  content=%d  sync=%s", f.torrents, f.content, f.lastSync)
			if f.lastErr != "" {
				stats += "  err=" + f.lastErr
			}
			box.Objects[1].(*widget.Label).SetText(stats)
			// Capture the full pubkey (not the row index) so a
			// recycled button that fires after a re-sort still
			// targets the publisher whose row was last rendered
			// into this widget.
			fullPK := f.pubkey
			box.Objects[2].(*widget.Button).OnTapped = func() {
				ct.unfollowByKey(fullPK)
			}
		},
	)
	ct.followList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(ct.follows) {
			ct.followSelectedKey = ""
			return
		}
		ct.followSelectedKey = ct.follows[id].pubkey
	}

	// Follow form.
	pubkeyEntry := widget.NewEntry()
	pubkeyEntry.SetPlaceHolder("64-char hex public key")
	labelEntry := widget.NewEntry()
	labelEntry.SetPlaceHolder("Label (e.g. MyIndexer)")

	followBtn := widget.NewButton("Follow", func() {
		ct.doFollow(pubkeyEntry.Text, labelEntry.Text)
	})

	followForm := widget.NewCard("Follow Publisher", "", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Public Key", pubkeyEntry),
			widget.NewFormItem("Label", labelEntry),
		),
		followBtn,
	))

	// Empty-state hint shown above/instead of the followList when
	// there are no follows yet. Gives the user an explanation of
	// what this panel is for rather than an empty rectangle.
	ct.followsEmpty = widget.NewLabelWithStyle(
		"No publishers followed yet. Paste a 64-char public key above\n"+
			"and press Follow to start syncing a remote Bleve index.",
		fyne.TextAlignCenter,
		fyne.TextStyle{Italic: true},
	)
	ct.followsEmpty.Wrapping = fyne.TextWrapWord

	// Stack the empty hint on top of the list; refresh() flips
	// visibility based on len(ct.follows).
	followListWithMenu := newRightClickCapture(ct.followList, ct.buildFollowMenu)
	followListArea := container.NewStack(followListWithMenu, ct.followsEmpty)
	followCard := widget.NewCard("Followed Publishers", "", followListArea)

	ct.content = container.NewVBox(
		pubCard,
		followForm,
		followCard,
	)

	return ct
}

func (ct *companionTab) pollLoop(ctx context.Context) {
	ct.refresh()
	tick := time.NewTicker(4 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			ct.refresh()
		}
	}
}

func (ct *companionTab) refresh() {
	// Publisher status.
	var pubKey, lastRefresh, lastIH, lastErr string
	var pubCount int
	if ct.d.CompPub != nil {
		st := ct.d.CompPub.Status()
		pubKey = st.PubKeyHex
		if !st.LastRefresh.IsZero() {
			lastRefresh = st.LastRefresh.Format(time.RFC3339)
		}
		lastIH = st.LastInfoHash
		lastErr = st.LastError
		pubCount = st.PublishedCount
	}

	// Follow list.
	var rows []followRow
	if ct.d.CompSub != nil {
		follows := ct.d.CompSub.Following()
		for pub, label := range follows {
			res := ct.d.CompSub.LastSync(pub)
			syncStr := "-"
			if res.GeneratedAt > 0 {
				syncStr = time.Unix(res.GeneratedAt, 0).UTC().Format(time.RFC3339)
			}
			errStr := ""
			if res.Err != nil {
				errStr = res.Err.Error()
			}
			rows = append(rows, followRow{
				pubkey:   hex.EncodeToString(pub[:]),
				label:    label,
				torrents: res.TorrentsImported,
				content:  res.ContentImported,
				lastSync: syncStr,
				lastErr:  errStr,
			})
		}
	}

	// CompSub.Following() returns a map, so iteration order is
	// non-deterministic. Sort by label then pubkey so a given
	// publisher always occupies the same screen position across
	// refreshes — without this the rows would visibly jump every
	// 4s and a stored selection would target the wrong publisher.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].label != rows[j].label {
			return rows[i].label < rows[j].label
		}
		return rows[i].pubkey < rows[j].pubkey
	})

	fyne.Do(func() {
		ct.pubKeyLbl.SetText(pubKey)
		ct.pubRefreshLbl.SetText(lastRefresh)
		ct.pubCountLbl.SetText(fmt.Sprintf("%d", pubCount))
		ct.pubInfoHashLbl.SetText(lastIH)
		if lastErr != "" {
			ct.pubErrorLbl.SetText("Error: " + lastErr)
		} else {
			ct.pubErrorLbl.SetText("")
		}
		ct.follows = rows
		ct.followList.Refresh()
		if len(rows) == 0 {
			ct.followsEmpty.Show()
		} else {
			ct.followsEmpty.Hide()
		}
	})
}

// afterRefreshPublisher, when non-nil, is invoked at the very end of the
// refreshPublisher goroutine (after any fyne.Do error render returns). It
// exists only so tests can deterministically join the async UI goroutine
// under the Fyne test driver, which runs fyne.Do callbacks inline on the
// calling goroutine. It is nil in production, so real-app timing/behavior
// is unchanged.
var afterRefreshPublisher func()

func (ct *companionTab) refreshPublisher() {
	if ct.d.CompPub == nil {
		return
	}
	go func() {
		err := ct.d.CompPub.RefreshNow()
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, ct.win())
			})
		}
		// fyne.Do under the test driver runs its callback inline+synchronously,
		// so by here any async render has finished. Signal the test seam (nil in
		// production) so tests can join this goroutine deterministically.
		if afterRefreshPublisher != nil {
			afterRefreshPublisher()
		}
	}()
}

func (ct *companionTab) doFollow(pubkeyHex, label string) {
	if ct.d.CompSub == nil {
		dialog.ShowError(fmt.Errorf("companion subscriber not configured"), ct.win())
		return
	}
	if len(pubkeyHex) != 64 {
		dialog.ShowError(fmt.Errorf("public key must be 64 hex characters"), ct.win())
		return
	}
	raw, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		dialog.ShowError(fmt.Errorf("invalid hex: %w", err), ct.win())
		return
	}
	var pub [32]byte
	copy(pub[:], raw)

	ct.d.CompSub.Follow(pub, label)
}

// buildFollowMenu returns the right-click context menu for the
// currently-selected follow row. Returns nil when no row has
// been clicked yet (rightClickCapture treats nil as "do
// nothing", so the menu silently no-ops on an empty list).
func (ct *companionTab) buildFollowMenu() *fyne.Menu {
	row, ok := ct.selectedRow()
	if !ok {
		return nil
	}
	pkHex := row.pubkey
	label := row.label
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Copy public key", func() {
			fyne.CurrentApp().Clipboard().SetContent(pkHex)
		}),
	}
	if label != "" {
		items = append(items, fyne.NewMenuItem("Copy label", func() {
			fyne.CurrentApp().Clipboard().SetContent(label)
		}))
	}
	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Unfollow", func() { ct.unfollowByKey(pkHex) }),
	)
	return fyne.NewMenu("Publisher actions", items...)
}

// selectedRow resolves the currently-selected follow row by its
// pubkey (followSelectedKey), scanning the live ct.follows slice.
// Returns ok=false when nothing is selected or the previously-
// selected publisher is no longer in the list (e.g. it was
// unfollowed or dropped between refreshes).
func (ct *companionTab) selectedRow() (followRow, bool) {
	if ct.followSelectedKey == "" {
		return followRow{}, false
	}
	for _, r := range ct.follows {
		if r.pubkey == ct.followSelectedKey {
			return r, true
		}
	}
	return followRow{}, false
}

// unfollowByKey unfollows the publisher identified by its pubkey
// hex. Keying by pubkey (rather than by a possibly-stale row index)
// guarantees we unfollow exactly the publisher the user clicked,
// even if the row order changed in the meantime.
func (ct *companionTab) unfollowByKey(pkHex string) {
	if ct.d.CompSub == nil || pkHex == "" {
		return
	}
	raw, err := hex.DecodeString(pkHex)
	if err != nil || len(raw) != 32 {
		return
	}
	var pub [32]byte
	copy(pub[:], raw)
	ct.d.CompSub.Unfollow(pub)
}

func (ct *companionTab) win() fyne.Window { return windowForObject(ct.content) }
