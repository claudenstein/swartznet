package gui

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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
	followList *widget.List
	follows    []followRow
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
	ct := &companionTab{d: d}

	// Publisher status labels.
	ct.pubKeyLbl = widget.NewLabel("-")
	ct.pubRefreshLbl = widget.NewLabel("-")
	ct.pubCountLbl = widget.NewLabel("-")
	ct.pubInfoHashLbl = widget.NewLabel("-")
	ct.pubErrorLbl = widget.NewLabel("")

	refreshBtn := widget.NewButton("Refresh Now", func() {
		ct.refreshPublisher()
	})

	pubCard := widget.NewCard("Companion Publisher", "", container.NewVBox(
		labelRow("Public Key:", ct.pubKeyLbl),
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
			pk := f.pubkey
			if len(pk) > 16 {
				pk = pk[:16] + "..."
			}
			box.Objects[0].(*widget.Label).SetText(fmt.Sprintf("%s (%s)", f.label, pk))
			stats := fmt.Sprintf("torrents=%d  content=%d  sync=%s", f.torrents, f.content, f.lastSync)
			if f.lastErr != "" {
				stats += "  err=" + f.lastErr
			}
			box.Objects[1].(*widget.Label).SetText(stats)
			box.Objects[2].(*widget.Button).OnTapped = func() {
				ct.unfollowAt(id)
			}
		},
	)

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

	followCard := widget.NewCard("Followed Publishers", "", ct.followList)

	ct.content = container.NewVBox(
		pubCard,
		followForm,
		followCard,
	)

	go ct.pollLoop(ctx)

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
		if len(pubKey) > 16 {
			pubKey = pubKey[:16] + "..."
		}
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
	})
}

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

func (ct *companionTab) unfollowAt(idx int) {
	if ct.d.CompSub == nil || idx >= len(ct.follows) {
		return
	}
	pkHex := ct.follows[idx].pubkey
	raw, err := hex.DecodeString(pkHex)
	if err != nil {
		return
	}
	var pub [32]byte
	copy(pub[:], raw)
	ct.d.CompSub.Unfollow(pub)
}

func (ct *companionTab) win() fyne.Window { return windowForObject(ct.content) }
