package gui

import (
	"fmt"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// showCreateDialog builds a .torrent from a file or folder — the GUI counterpart
// of `swartznet create`. It optionally signs the file with the node's identity
// and seeds the content in place through the RUNNING engine (so the new torrent
// appears in the Downloads list immediately). Hashing runs off the UI thread
// behind a modal progress indicator.
func (dl *downloadsTab) showCreateDialog() {
	if dl.d == nil || dl.d.Eng == nil {
		return
	}
	win := dl.window()
	if win == nil {
		return
	}

	source := widget.NewEntry()
	source.SetPlaceHolder("file or folder to share")
	output := widget.NewEntry()
	output.SetPlaceHolder("output .torrent path (defaults next to the source)")

	// Auto-derive the output path from the source, RE-deriving whenever the source
	// changes — but never clobbering a path the user typed. We remember the value
	// WE last auto-filled; if output still holds exactly that, the user has not
	// touched it, so refreshing it is safe. (No file-picker for the output: Fyne's
	// save dialog os.Create()s the chosen file immediately, which would truncate an
	// existing file to zero bytes if the user then cancelled.)
	lastDerived := ""
	source.OnChanged = func(string) {
		if output.Text != lastDerived {
			return // user edited the output path; leave it
		}
		derived := deriveTorrentPath(source.Text)
		lastDerived = derived
		output.SetText(derived)
	}

	pickFile := widget.NewButton("File…", func() {
		dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			defer rc.Close()
			source.SetText(rc.URI().Path())
		}, win)
	})
	pickFolder := widget.NewButton("Folder…", func() {
		dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
			if err != nil || u == nil {
				return
			}
			source.SetText(u.Path())
		}, win)
	})

	trackers := widget.NewEntry()
	trackers.SetPlaceHolder("optional tracker URLs (space or comma separated)")
	comment := widget.NewEntry()
	comment.SetPlaceHolder("optional comment")
	privateChk := widget.NewCheck("Private (BEP-27: no DHT / PEX)", nil)
	seedChk := widget.NewCheck("Seed the content after creating", nil)
	seedChk.SetChecked(true)
	signChk := widget.NewCheck("Sign with my identity", nil)
	if dl.d.Identity == nil {
		signChk.SetText("Sign with my identity  (no identity loaded)")
		signChk.Disable()
	}

	// A labelled row: fixed-width label, an entry that expands, optional trailing buttons.
	labeled := func(label string, field fyne.CanvasObject, trailing ...fyne.CanvasObject) fyne.CanvasObject {
		var right fyne.CanvasObject
		if len(trailing) > 0 {
			right = container.NewHBox(trailing...)
		}
		return container.NewBorder(nil, nil, widget.NewLabel(label), right, field)
	}

	content := container.NewVBox(
		labeled("Source", source, pickFile, pickFolder),
		labeled("Output", output),
		labeled("Trackers", trackers),
		labeled("Comment", comment),
		privateChk, seedChk, signChk,
	)

	form := dialog.NewCustomConfirm("Create torrent", "Create", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		root := strings.TrimSpace(source.Text)
		out := strings.TrimSpace(output.Text)
		if root == "" || out == "" {
			dl.showErr(fmt.Errorf("both a source file/folder and an output .torrent path are required"))
			return
		}
		opts := engine.CreateTorrentOptions{
			Root:      root,
			Trackers:  splitList(trackers.Text),
			Private:   privateChk.Checked,
			Comment:   strings.TrimSpace(comment.Text),
			CreatedBy: "swartznet (gui)",
		}
		if signChk.Checked && dl.d.Identity != nil {
			s := dl.d.Identity.Signer()
			opts.SignWith = &s
		}
		doSeed := seedChk.Checked

		// Hashing a large folder can take a while: run it off the UI thread behind
		// a modal progress bar, then report the result on the UI thread.
		prog := dialog.NewCustomWithoutButtons("Creating torrent…", widget.NewProgressBarInfinite(), win)
		prog.Show()
		go func() {
			ihHex, raw, err := engine.CreateTorrentFile(opts, out)
			var seedErr error
			if err == nil && doSeed {
				_, seedErr = dl.d.Eng.AddTorrentBytesSeedFrom(raw, root)
			}
			fyne.Do(func() {
				prog.Hide()
				if err != nil {
					dl.showErr(fmt.Errorf("create torrent: %w", err))
					return
				}
				dl.refresh()
				if seedErr != nil {
					dl.showErr(fmt.Errorf("created %s (infohash %s) but seeding failed: %w", out, ihHex, seedErr))
					return
				}
				msg := fmt.Sprintf("Created %s\nInfoHash: %s", out, ihHex)
				if doSeed {
					msg += "\nNow seeding the content."
				}
				dialog.ShowInformation("Torrent created", msg, win)
			})
		}()
	}, win)
	form.Resize(fyne.NewSize(660, 380))
	form.Show()
}

// deriveTorrentPath returns the default output ".torrent" path for a source path
// (its cleaned value plus ".torrent"), or "" for an empty source. Trailing path
// separators are trimmed so a folder like "/a/b/" maps to "/a/b.torrent".
func deriveTorrentPath(source string) string {
	src := strings.TrimRight(strings.TrimSpace(source), string(filepath.Separator))
	if src == "" {
		return ""
	}
	return src + ".torrent"
}

// splitList splits a space/comma/newline/tab-separated list into non-empty,
// trimmed fields (or nil). Used for the optional tracker list.
func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
