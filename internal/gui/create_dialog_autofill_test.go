package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCreateTorrentDialogAutofillsOutput verifies the Create
// Torrent dialog pre-populates the Output path entry with
// "<root>.torrent" when the user types into the Root entry.
// This was added to eliminate the "Output path required" UX
// papercut where users picked a root, hit Create, and got an
// error.
func TestCreateTorrentDialogAutofillsOutput(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	createTorrentDialog(d, w)

	findEntry := func(placeholder string) *widget.Entry {
		for _, ov := range w.Canvas().Overlays().List() {
			for _, child := range test.LaidOutObjects(ov) {
				if e, ok := child.(*widget.Entry); ok && e.PlaceHolder == placeholder {
					return e
				}
			}
		}
		return nil
	}

	rootE := findEntry("/path/to/file-or-folder")
	outE := findEntry("/path/to/output.torrent")
	if rootE == nil || outE == nil {
		t.Fatalf("dialog entries not found in overlay tree")
	}

	rootE.SetText("/some/dir/payload")
	if got := outE.Text; got != "/some/dir/payload.torrent" {
		t.Errorf("autofill output = %q, want %q", got, "/some/dir/payload.torrent")
	}

	// User customizes the output. Subsequent root edits must NOT
	// overwrite the user's customization.
	outE.SetText("/elsewhere/manual.torrent")
	rootE.SetText("/some/dir/another")
	if got := outE.Text; got != "/elsewhere/manual.torrent" {
		t.Errorf("user-edited output overwritten: got %q", got)
	}

	// Trailing slash on the root (folder mode) → strip it before
	// appending .torrent.
	outE.SetText("")
	rootE.SetText("/some/dir/folder/")
	if got := outE.Text; got != "/some/dir/folder.torrent" {
		t.Errorf("trailing-slash autofill = %q, want %q", got, "/some/dir/folder.torrent")
	}
}
