package gui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

type settingsTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon

	shareRadio  *widget.RadioGroup
	fileHitsChk *widget.Check
	contentChk  *widget.Check
}

var shareLevels = []string{
	"L0 - Don't answer queries",
	"L1 - In-swarm peers only",
	"L2 - Full local index",
}

func newSettingsTab(d *daemon.Daemon) *settingsTab {
	st := &settingsTab{d: d}

	st.shareRadio = widget.NewRadioGroup(shareLevels, nil)
	st.fileHitsChk = widget.NewCheck("Share file paths in search results", nil)
	st.contentChk = widget.NewCheck("Share content text snippets", nil)

	// Load current capabilities.
	st.loadCurrent()

	saveBtn := widget.NewButton("Save", func() {
		st.save()
	})

	sharingCard := widget.NewCard("Sharing Capabilities", "Controls what this node shares with sn_search peers", container.NewVBox(
		widget.NewLabel("Share level:"),
		st.shareRadio,
		widget.NewSeparator(),
		st.fileHitsChk,
		st.contentChk,
		widget.NewSeparator(),
		saveBtn,
	))

	// Info card.
	cfgInfo := widget.NewCard("Configuration", "", container.NewVBox(
		labelRow("Data directory:", widget.NewLabel(d.Cfg.DataDir)),
		labelRow("Index directory:", widget.NewLabel(d.Cfg.IndexDir)),
		labelRow("Listen port:", widget.NewLabel(portStr(d.Cfg.ListenPort))),
		labelRow("DHT:", widget.NewLabel(boolStr(!d.Cfg.DisableDHT))),
	))

	st.content = container.NewVBox(sharingCard, cfgInfo)

	return st
}

func (st *settingsTab) loadCurrent() {
	sw := st.d.Eng.SwarmSearch()
	if sw == nil {
		return
	}
	caps := sw.Capabilities()
	switch caps.ShareLocal {
	case 0:
		st.shareRadio.SetSelected(shareLevels[0])
	case 1:
		st.shareRadio.SetSelected(shareLevels[1])
	default:
		st.shareRadio.SetSelected(shareLevels[2])
	}
	st.fileHitsChk.SetChecked(caps.FileHits == 1)
	st.contentChk.SetChecked(caps.ContentHits == 1)
}

func (st *settingsTab) save() {
	sw := st.d.Eng.SwarmSearch()
	if sw == nil {
		return
	}

	var shareLocal int
	switch st.shareRadio.Selected {
	case shareLevels[0]:
		shareLocal = 0
	case shareLevels[1]:
		shareLocal = 1
	case shareLevels[2]:
		shareLocal = 2
	}

	caps := swarmsearch.Capabilities{
		ShareLocal:  shareLocal,
		FileHits:    boolInt(st.fileHitsChk.Checked),
		ContentHits: boolInt(st.contentChk.Checked),
	}
	sw.SetCapabilities(caps)

	dialog.ShowInformation("Saved", "Sharing capabilities updated", st.win())
}

func (st *settingsTab) win() fyne.Window {
	c := fyne.CurrentApp().Driver().CanvasForObject(st.content)
	if c == nil {
		for _, w := range fyne.CurrentApp().Driver().AllWindows() {
			return w
		}
		return nil
	}
	for _, w := range fyne.CurrentApp().Driver().AllWindows() {
		if w.Canvas() == c {
			return w
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func boolStr(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func portStr(p int) string {
	if p == 0 {
		return "OS-assigned"
	}
	return fmt.Sprintf("%d", p)
}
