package gui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/daemon"
)

// licenseLine is the first-party license shown in About. SwartzNet code is
// Apache-2.0 (the legacy §6 defect claimed "MIT"); the MPL-2.0 anacrolix engine
// dependency is noted separately.
const licenseLine = "Apache-2.0 (SwartzNet) · engine anacrolix/torrent: MPL-2.0"

// settingsTab exposes rate limits, the sharing/capabilities prefs, and the
// About/version dialog. No poll loop — settings are read once and on save.
type settingsTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon
	win     func() fyne.Window
}

func newSettingsTab(d *daemon.Daemon) *settingsTab {
	se := &settingsTab{d: d}

	ulEntry := widget.NewEntry()
	dlEntry := widget.NewEntry()
	maxEntry := widget.NewEntry()
	if e := d.Eng; e != nil {
		ulEntry.SetText(strconv.FormatInt(e.UploadLimitBytesPerSec(), 10))
		dlEntry.SetText(strconv.FormatInt(e.DownloadLimitBytesPerSec(), 10))
		maxEntry.SetText(strconv.Itoa(e.MaxActiveDownloads()))
	}
	saveRates := widget.NewButton("Apply", func() {
		if e := d.Eng; e != nil {
			e.SetUploadLimitBytesPerSec(parseInt64(ulEntry.Text))
			e.SetDownloadLimitBytesPerSec(parseInt64(dlEntry.Text))
			e.SetMaxActiveDownloads(int(parseInt64(maxEntry.Text)))
		}
	})
	rates := widget.NewCard("Rate limits & queue", "0 = unlimited", container.NewVBox(
		labeledRow("Upload B/s", ulEntry),
		labeledRow("Download B/s", dlEntry),
		labeledRow("Max active downloads", maxEntry),
		saveRates,
	))

	// Sharing prefs (the operator half of the sn_search capability mask).
	shareLocal := widget.NewCheck("Answer local-index queries (ShareLocal=2)", nil)
	fileHits := widget.NewCheck("Share file hits", nil)
	contentHits := widget.NewCheck("Share content hits", nil)
	if e := d.Eng; e != nil {
		s := e.Sharing()
		shareLocal.SetChecked(s.ShareLocal > 0)
		fileHits.SetChecked(s.FileHits)
		contentHits.SetChecked(s.ContentHits)
	}
	saveShare := widget.NewButton("Apply", func() {
		if e := d.Eng; e != nil {
			s := ltepwire.Sharing{FileHits: fileHits.Checked, ContentHits: contentHits.Checked}
			if shareLocal.Checked {
				s.ShareLocal = 2
			}
			e.SetSharing(s)
		}
	})
	sharing := widget.NewCard("Sharing (sn_search)", "", container.NewVBox(shareLocal, fileHits, contentHits, saveShare))

	aboutBtn := widget.NewButton("About SwartzNet", func() {
		if w := se.window(); w != nil {
			ShowAbout(w, se.d, "", "")
		}
	})

	se.content = container.NewVScroll(container.NewVBox(rates, sharing, aboutBtn))
	return se
}

// ShowAbout renders the About dialog with the corrected first-party license.
// version/buildDate come from the single build-stamped source (main.go); an
// empty buildDate renders "(dev build)".
func ShowAbout(w fyne.Window, d *daemon.Daemon, version, buildDate string) {
	if buildDate == "" {
		buildDate = "(dev build)"
	}
	pubKey, port, api := "unknown", "unknown", "disabled"
	if d != nil && d.Identity != nil {
		pubKey = d.Identity.PublicKeyHex()
	}
	if d != nil && d.Eng != nil {
		if p := d.Eng.LocalPort(); p > 0 {
			port = strconv.Itoa(p)
		}
	}
	if d != nil && d.API != nil {
		if a := d.API.Addr(); a != "" {
			api = a
		}
	}
	items := []*widget.FormItem{
		widget.NewFormItem("Version", widget.NewLabel(orDash(version))),
		widget.NewFormItem("Built", widget.NewLabel(buildDate)),
		widget.NewFormItem("Identity", widget.NewLabel(pubKey)),
		widget.NewFormItem("BitTorrent port", widget.NewLabel(port)),
		widget.NewFormItem("HTTP API", widget.NewLabel(api)),
		widget.NewFormItem("License", widget.NewLabel(licenseLine)),
	}
	dialog.ShowForm("About SwartzNet", "Close", "", items, func(bool) {}, w)
}

func labeledRow(label string, e *widget.Entry) fyne.CanvasObject {
	return container.NewBorder(nil, nil, widget.NewLabel(label), nil, e)
}

func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (se *settingsTab) window() fyne.Window {
	if se.win != nil {
		return se.win()
	}
	return nil
}

var _ = fmt.Sprintf
