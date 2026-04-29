package gui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

type settingsTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon

	shareRadio  *widget.RadioGroup
	fileHitsChk *widget.Check
	contentChk  *widget.Check

	uploadEntry   *widget.Entry
	downloadEntry *widget.Entry

	maxActiveEntry *widget.Entry
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

	// Rate limiting card.
	st.uploadEntry = widget.NewEntry()
	st.uploadEntry.SetPlaceHolder("0 (unlimited)")
	st.downloadEntry = widget.NewEntry()
	st.downloadEntry.SetPlaceHolder("0 (unlimited)")
	st.loadRateLimits()

	applyLimitsBtn := widget.NewButton("Apply", func() {
		st.applyRateLimits()
	})

	rateCard := widget.NewCard("Bandwidth Limits", "Zero means unlimited. Applies immediately.", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Download (KiB/s)", st.downloadEntry),
			widget.NewFormItem("Upload (KiB/s)", st.uploadEntry),
		),
		applyLimitsBtn,
	))

	// Queue management card.
	st.maxActiveEntry = widget.NewEntry()
	st.maxActiveEntry.SetPlaceHolder("0 (unlimited)")
	st.loadQueueSettings()
	applyQueueBtn := widget.NewButton("Apply", func() {
		st.applyQueueSettings()
	})
	queueCard := widget.NewCard("Queue Management", "Cap concurrent active downloads. Zero means unlimited. Paused/complete torrents don't count.", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Max active downloads", st.maxActiveEntry),
		),
		applyQueueBtn,
	))

	// Storage paths card — DataDir / IndexDir live on disk and
	// are wired into the engine + indexer at startup, so changing
	// them requires a restart. We persist edits to the user
	// config file and tell the user to restart for them to take
	// effect; reflecting them live would mean tearing down and
	// re-bringing-up the engine, indexer, and companion publisher
	// in-place, which is not safe to do while torrents are
	// active.
	dataDirEntry := widget.NewEntry()
	dataDirEntry.SetText(d.Cfg.DataDir)
	indexDirEntry := widget.NewEntry()
	indexDirEntry.SetText(d.Cfg.IndexDir)

	browseDataBtn := widget.NewButton("Browse...", func() {
		fd := dialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil || lu == nil {
				return
			}
			dataDirEntry.SetText(lu.Path())
		}, st.win())
		if cur := strings.TrimSpace(dataDirEntry.Text); cur != "" {
			if loc, err := storage.ListerForURI(storage.NewFileURI(cur)); err == nil {
				fd.SetLocation(loc)
			}
		}
		fd.Show()
	})
	browseIndexBtn := widget.NewButton("Browse...", func() {
		fd := dialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil || lu == nil {
				return
			}
			indexDirEntry.SetText(lu.Path())
		}, st.win())
		if cur := strings.TrimSpace(indexDirEntry.Text); cur != "" {
			if loc, err := storage.ListerForURI(storage.NewFileURI(cur)); err == nil {
				fd.SetLocation(loc)
			}
		}
		fd.Show()
	})

	saveDirsBtn := widget.NewButton("Save", func() {
		st.savePaths(dataDirEntry.Text, indexDirEntry.Text)
	})
	resetDirsBtn := widget.NewButton("Reset to defaults", func() {
		def := config.Default()
		dataDirEntry.SetText(def.DataDir)
		indexDirEntry.SetText(def.IndexDir)
	})

	dataRow := container.NewBorder(nil, nil, nil, browseDataBtn, dataDirEntry)
	indexRow := container.NewBorder(nil, nil, nil, browseIndexBtn, indexDirEntry)

	dirsCard := widget.NewCard(
		"Storage Paths",
		"Where downloaded content and the local search index live. Changes take effect on next restart.",
		container.NewVBox(
			widget.NewLabel("Data directory"),
			dataRow,
			widget.NewLabel("Index directory"),
			indexRow,
			container.NewHBox(saveDirsBtn, resetDirsBtn),
		),
	)

	// Read-only details for the rest of the runtime config.
	cfgInfo := widget.NewCard("Runtime", "", container.NewVBox(
		labelRow("Listen port:", widget.NewLabel(portStr(d.Cfg.ListenPort))),
		labelRow("DHT:", widget.NewLabel(boolStr(!d.Cfg.DisableDHT))),
	))

	st.content = container.NewVBox(sharingCard, rateCard, queueCard, dirsCard, cfgInfo)

	return st
}

// savePaths persists the user's edits to data/index directories
// and tells the user a restart is needed to pick them up.
// Refusing empty values keeps the next launch from falling back
// to "" → Validate failure on startup.
func (st *settingsTab) savePaths(dataDir, indexDir string) {
	dataDir = strings.TrimSpace(dataDir)
	indexDir = strings.TrimSpace(indexDir)
	if dataDir == "" {
		dialog.ShowError(fmt.Errorf("data directory must not be empty"), st.win())
		return
	}
	if indexDir == "" {
		dialog.ShowError(fmt.Errorf("index directory must not be empty"), st.win())
		return
	}
	if err := config.SaveUserOverrides(config.DefaultUserConfigPath(), dataDir, indexDir); err != nil {
		dialog.ShowError(err, st.win())
		return
	}
	dialog.ShowInformation(
		"Saved",
		"Data and index directories saved.\n\nRestart SwartzNet to switch over to the new paths. Existing torrents and indexed content stay where they are; the new paths apply to future downloads and indexing only.",
		st.win(),
	)
}

func (st *settingsTab) loadQueueSettings() {
	n := st.d.Eng.MaxActiveDownloads()
	st.maxActiveEntry.SetText(strconv.Itoa(n))
}

func (st *settingsTab) applyQueueSettings() {
	s := strings.TrimSpace(st.maxActiveEntry.Text)
	if s == "" {
		s = "0"
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		dialog.ShowError(fmt.Errorf("must be a non-negative integer"), st.win())
		return
	}
	st.d.Eng.SetMaxActiveDownloads(n)
	label := strconv.Itoa(n)
	if n == 0 {
		label = "unlimited"
	}
	dialog.ShowInformation("Saved",
		fmt.Sprintf("Max active downloads: %s", label), st.win())
}

func (st *settingsTab) loadRateLimits() {
	ul := st.d.Eng.UploadLimitBytesPerSec()
	dl := st.d.Eng.DownloadLimitBytesPerSec()
	st.uploadEntry.SetText(kibStr(ul))
	st.downloadEntry.SetText(kibStr(dl))
}

func (st *settingsTab) applyRateLimits() {
	ulKiB, err := parseKiB(st.uploadEntry.Text)
	if err != nil {
		dialog.ShowError(fmt.Errorf("upload: %w", err), st.win())
		return
	}
	dlKiB, err := parseKiB(st.downloadEntry.Text)
	if err != nil {
		dialog.ShowError(fmt.Errorf("download: %w", err), st.win())
		return
	}
	st.d.Eng.SetUploadLimitBytesPerSec(ulKiB * 1024)
	st.d.Eng.SetDownloadLimitBytesPerSec(dlKiB * 1024)
	dialog.ShowInformation("Saved",
		fmt.Sprintf("Upload: %s KiB/s\nDownload: %s KiB/s",
			limitDisplay(ulKiB), limitDisplay(dlKiB)), st.win())
}

// parseKiB accepts an empty string (= 0) or a non-negative integer
// count of KiB/s. Returns the parsed value in KiB/s.
func parseKiB(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a whole number")
	}
	if v < 0 {
		return 0, fmt.Errorf("must be ≥ 0")
	}
	return v, nil
}

func kibStr(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return "0"
	}
	return strconv.FormatInt(bytesPerSec/1024, 10)
}

func limitDisplay(kib int64) string {
	if kib == 0 {
		return "unlimited"
	}
	return strconv.FormatInt(kib, 10)
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

func (st *settingsTab) win() fyne.Window { return windowForObject(st.content) }

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
