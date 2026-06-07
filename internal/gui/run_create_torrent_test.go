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
// (ShowError + return). We join the goroutine via the
// afterCreateTorrent seam so its inline fyne.Do render finishes
// before the test returns, keeping rendering single-threaded under
// the Fyne test driver.
func TestRunCreateTorrentMissingRoot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	done := make(chan struct{}, 1)
	afterCreateTorrent = func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { afterCreateTorrent = nil })

	runCreateTorrent(d, w, engine.CreateTorrentOptions{
		Root: filepath.Join(t.TempDir(), "does-not-exist"),
		Name: "test",
	}, filepath.Join(t.TempDir(), "out.torrent"), false)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCreateTorrent goroutine did not complete")
	}
}
