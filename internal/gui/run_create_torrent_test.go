package gui

import (
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestRunCreateTorrentMissingRoot covers runCreateTorrent at
// create.go:225-257. With a non-existent Root path, CreateTorrentFile
// errs and the goroutine's fyne.Do callback hits the err arm
// (ShowError + return). We sleep generously at end so the
// goroutine fully drains before the test returns.
func TestRunCreateTorrentMissingRoot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	runCreateTorrent(d, w, engine.CreateTorrentOptions{
		Root: filepath.Join(t.TempDir(), "does-not-exist"),
		Name: "test",
	}, filepath.Join(t.TempDir(), "out.torrent"), false)

	// Drain the goroutine: it calls CreateTorrentFile which errs
	// quickly on missing path, then fyne.Do(ShowError). 500ms is
	// far more than needed.
	time.Sleep(500 * time.Millisecond)
}
