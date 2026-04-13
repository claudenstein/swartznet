package gui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/engine"
)

// pieceLengthOptions mirrors common BitTorrent client presets.
// Zero means "auto" (uses metainfo.ChoosePieceLength).
var pieceLengthOptions = []struct {
	label string
	value int64
}{
	{"Auto", 0},
	{"64 KiB", 64 * 1024},
	{"256 KiB", 256 * 1024},
	{"1 MiB", 1 << 20},
	{"2 MiB", 2 << 20},
	{"4 MiB", 4 << 20},
	{"8 MiB", 8 << 20},
	{"16 MiB", 16 << 20},
}

func pieceLengthLabels() []string {
	out := make([]string, len(pieceLengthOptions))
	for i, o := range pieceLengthOptions {
		out[i] = o.label
	}
	return out
}

func pieceLengthFromLabel(label string) int64 {
	for _, o := range pieceLengthOptions {
		if o.label == label {
			return o.value
		}
	}
	return 0
}

// createTorrentDialog shows the Create Torrent modal.
// Hashing runs in a background goroutine so the UI stays live;
// progress is reported via a modal progress dialog the goroutine
// dismisses on completion.
func createTorrentDialog(d *daemon.Daemon, win fyne.Window) {
	rootEntry := widget.NewEntry()
	rootEntry.SetPlaceHolder("/path/to/file-or-folder")

	browseFileBtn := widget.NewButton("Choose File...", func() {
		fd := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil || r == nil {
				return
			}
			rootEntry.SetText(r.URI().Path())
			r.Close()
		}, win)
		fd.Show()
	})
	browseFolderBtn := widget.NewButton("Choose Folder...", func() {
		fd := dialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil || lu == nil {
				return
			}
			rootEntry.SetText(lu.Path())
		}, win)
		fd.Show()
	})
	rootRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(browseFileBtn, browseFolderBtn),
		rootEntry)

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Leave empty to use the basename")

	pieceSelect := widget.NewSelect(pieceLengthLabels(), nil)
	pieceSelect.SetSelected("Auto")

	trackersEntry := widget.NewMultiLineEntry()
	trackersEntry.SetPlaceHolder("One tracker URL per line (optional — empty = DHT-only)")
	trackersEntry.SetMinRowsVisible(3)

	webseedsEntry := widget.NewMultiLineEntry()
	webseedsEntry.SetPlaceHolder("Optional: HTTP(S) webseed URLs, one per line (BEP-19)")
	webseedsEntry.SetMinRowsVisible(2)

	commentEntry := widget.NewEntry()
	commentEntry.SetPlaceHolder("Optional human-readable comment")

	privateCheck := widget.NewCheck("Private torrent (disable DHT/PEX discovery, BEP-27)", nil)

	signCheck := widget.NewCheck("Sign with my ed25519 identity (SwartzNet downloaders can verify publisher)", nil)
	// Default on: every running SwartzNet node has an identity
	// available, and signing costs effectively nothing.
	signCheck.SetChecked(true)

	seedCheck := widget.NewCheck("Start seeding immediately after creation", nil)
	seedCheck.SetChecked(true)

	outEntry := widget.NewEntry()
	outEntry.SetPlaceHolder("/path/to/output.torrent")
	browseOutBtn := widget.NewButton("Save As...", func() {
		fd := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
			if err != nil || wc == nil {
				return
			}
			outEntry.SetText(wc.URI().Path())
			// We don't actually want to write via the Fyne writer;
			// close it without writing so the real CreateTorrentFile
			// path handles the atomic rename itself.
			_ = wc.Close()
			// Remove the empty file Fyne created (best-effort; user
			// may have picked a brand-new path).
			_ = storage.Delete(wc.URI())
		}, win)
		fd.SetFileName("new.torrent")
		fd.Show()
	})
	outRow := container.NewBorder(nil, nil, nil, browseOutBtn, outEntry)

	form := container.NewVBox(
		widget.NewCard("Source", "", container.NewVBox(
			widget.NewLabel("Root (file or folder to share)"),
			rootRow,
			widget.NewLabel("Name (optional)"),
			nameEntry,
		)),
		widget.NewCard("Pieces & Metadata", "", container.NewVBox(
			widget.NewLabel("Piece length"),
			pieceSelect,
			widget.NewLabel("Trackers"),
			trackersEntry,
			widget.NewLabel("Webseeds"),
			webseedsEntry,
			widget.NewLabel("Comment"),
			commentEntry,
			privateCheck,
			signCheck,
		)),
		widget.NewCard("Output", "", container.NewVBox(
			widget.NewLabel("Output .torrent path"),
			outRow,
			seedCheck,
		)),
	)

	scroll := container.NewVScroll(form)
	scroll.SetMinSize(fyne.NewSize(600, 500))

	dlg := dialog.NewCustomConfirm(
		"Create Torrent",
		"Create",
		"Cancel",
		scroll,
		func(ok bool) {
			if !ok {
				return
			}
			if strings.TrimSpace(rootEntry.Text) == "" {
				dialog.ShowError(fmt.Errorf("root path required"), win)
				return
			}
			if strings.TrimSpace(outEntry.Text) == "" {
				dialog.ShowError(fmt.Errorf("output path required"), win)
				return
			}
			opts := engine.CreateTorrentOptions{
				Root:        strings.TrimSpace(rootEntry.Text),
				Name:        strings.TrimSpace(nameEntry.Text),
				PieceLength: pieceLengthFromLabel(pieceSelect.Selected),
				Trackers:    splitLines(trackersEntry.Text),
				WebSeeds:    splitLines(webseedsEntry.Text),
				Private:     privateCheck.Checked,
				Comment:     strings.TrimSpace(commentEntry.Text),
			}
			if signCheck.Checked {
				if id := d.Eng.Identity(); id != nil {
					opts.SignWith = id.PrivateKey
				}
			}
			runCreateTorrent(d, win, opts, strings.TrimSpace(outEntry.Text), seedCheck.Checked)
		},
		win,
	)
	dlg.Resize(fyne.NewSize(650, 600))
	dlg.Show()
}

// runCreateTorrent spawns the hashing goroutine and shows a
// progress dialog until it completes.
func runCreateTorrent(d *daemon.Daemon, win fyne.Window, opts engine.CreateTorrentOptions, outPath string, andSeed bool) {
	progress := dialog.NewCustomWithoutButtons(
		"Hashing pieces...",
		container.NewVBox(
			widget.NewLabel("Reading "+opts.Root),
			widget.NewProgressBarInfinite(),
			widget.NewLabel("Large torrents can take several minutes."),
		),
		win,
	)
	progress.Show()

	go func() {
		ih, mi, err := d.Eng.CreateTorrentFile(opts, outPath)
		fyne.Do(func() {
			progress.Hide()
			if err != nil {
				dialog.ShowError(err, win)
				return
			}

			msg := fmt.Sprintf("Created:\n  %s\n\nInfoHash:\n  %s", outPath, ih)
			if andSeed && mi != nil {
				if _, err := d.Eng.AddTorrentMetaInfo(mi); err != nil {
					msg += "\n\nSeed start failed: " + err.Error()
				} else {
					msg += "\n\nSeeding started."
				}
			}
			dialog.ShowInformation("Torrent created", msg, win)
		})
	}()
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	return out
}
