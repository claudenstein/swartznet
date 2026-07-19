package gui

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

const companionPollInterval = 3 * time.Second

// companionTab renders the companion publisher status + follow list, driving
// follow / unfollow / refresh through the daemon's companion workers.
type companionTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon
	pubLbl  *widget.Label
	follows *widget.Label
	win     func() fyne.Window
}

func newCompanionTab(ctx context.Context, d *daemon.Daemon) *companionTab {
	cp := &companionTab{d: d}
	cp.pubLbl = widget.NewLabel("")
	cp.pubLbl.TextStyle.Monospace = true
	cp.follows = widget.NewLabel("")
	cp.follows.TextStyle.Monospace = true

	followBtn := widget.NewButtonWithIcon("Follow publisher", theme.ContentAddIcon(), cp.showFollowDialog)
	refreshBtn := widget.NewButtonWithIcon("Re-publish now", theme.ViewRefreshIcon(), cp.refreshNow)
	toolbar := container.NewHBox(followBtn, refreshBtn)

	body := container.NewVBox(
		widget.NewCard("Publisher", "", cp.pubLbl),
		widget.NewCard("Following", "", cp.follows),
	)
	cp.content = container.NewBorder(toolbar, nil, nil, nil, container.NewVScroll(body))
	cp.refresh()
	go cp.pollLoop(ctx)
	return cp
}

func (cp *companionTab) pollLoop(ctx context.Context) {
	t := time.NewTicker(companionPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fyne.Do(cp.refresh)
		}
	}
}

func (cp *companionTab) refresh() {
	if cp.d.CompPub == nil {
		cp.pubLbl.SetText("(publisher not started — needs an identity, the DHT, and a companion dir)")
	} else {
		s := cp.d.CompPub.Status()
		lines := []string{"pubkey:    " + s.PubKeyHex,
			fmt.Sprintf("published: %d time(s)", s.PublishedCount)}
		if s.LastInfoHash != "" {
			lines = append(lines, "last:      "+s.LastInfoHash)
		}
		if !s.LastRefresh.IsZero() {
			lines = append(lines, "refreshed: "+s.LastRefresh.Format(time.RFC3339))
		}
		if s.LastError != "" {
			lines = append(lines, "error:     "+s.LastError)
		}
		cp.pubLbl.SetText(joinLines(lines))
	}

	if cp.d.CompSub == nil {
		cp.follows.SetText("(subscriber not started)")
		return
	}
	following := cp.d.CompSub.Following()
	if len(following) == 0 {
		cp.follows.SetText("(not following anyone)")
		return
	}
	var lines []string
	for pub, label := range following {
		res := cp.d.CompSub.LastSync(pub)
		l := hex.EncodeToString(pub[:])
		if label != "" {
			l += " (" + label + ")"
		}
		l += fmt.Sprintf("  torrents=%d content=%d", res.TorrentsImported, res.ContentImported)
		if res.Err != nil {
			l += "  err=" + res.Err.Error()
		}
		lines = append(lines, l)
	}
	cp.follows.SetText(joinLines(lines))
}

func (cp *companionTab) showFollowDialog() {
	if cp.d.CompSub == nil {
		cp.showErr(fmt.Errorf("companion subscriber not started"))
		return
	}
	pkEntry := widget.NewEntry()
	pkEntry.SetPlaceHolder("64-hex publisher pubkey")
	labelEntry := widget.NewEntry()
	labelEntry.SetPlaceHolder("optional label")
	dialog.NewForm("Follow a publisher", "Follow", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Pubkey", pkEntry),
		widget.NewFormItem("Label", labelEntry),
	}, func(ok bool) {
		if !ok {
			return
		}
		pkHex := strings.ToLower(strings.TrimSpace(pkEntry.Text))
		raw, err := hex.DecodeString(pkHex)
		if err != nil || len(raw) != 32 {
			cp.showErr(fmt.Errorf("pubkey must be 64 hex characters"))
			return
		}
		var pub [32]byte
		copy(pub[:], raw)
		cp.d.CompSub.Follow(pub, strings.TrimSpace(labelEntry.Text))
		cp.refresh()
	}, cp.window()).Show()
}

func (cp *companionTab) refreshNow() {
	if cp.d.CompPub == nil {
		cp.showErr(fmt.Errorf("companion publisher not started"))
		return
	}
	go func() {
		err := cp.d.CompPub.RefreshNow()
		fyne.Do(func() {
			if err != nil {
				cp.showErr(err)
			}
			cp.refresh()
		})
	}()
}

func (cp *companionTab) window() fyne.Window {
	if cp.win != nil {
		return cp.win()
	}
	return nil
}

func (cp *companionTab) showErr(err error) {
	if w := cp.window(); w != nil {
		dialog.ShowError(err, w)
	}
}
