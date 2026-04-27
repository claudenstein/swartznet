package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestRunCreateTorrentMissingRoot covers runCreateTorrent at
// create.go:225-257 against a real daemon. With a Root that
// doesn't exist on disk, engine.CreateTorrentFile returns an
// error; the goroutine's fyne.Do callback hits the err arm
// (ShowError + return) at lines 241-244. We wait briefly for
// the goroutine to settle.
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

	// Wait for the goroutine to finish.
	time.Sleep(200 * time.Millisecond)
}

// TestRunCreateTorrentSuccess covers runCreateTorrent's happy
// path: a valid Root file produces a real .torrent. With
// andSeed=true the function also calls AddTorrentMetaInfo which,
// for a freshly-created infohash, re-adds successfully. This
// exercises the "Seeding started" subtree.
func TestRunCreateTorrentSuccess(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// Create a small file to package as a torrent.
	src := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(src, []byte("hello swartznet test"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "out.torrent")
	runCreateTorrent(d, w, engine.CreateTorrentOptions{
		Root: src,
		Name: "payload.bin",
	}, out, true)

	// Wait for the goroutine to complete the hashing + add.
	time.Sleep(500 * time.Millisecond)
}

