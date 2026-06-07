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

// TestRunCreateTorrentSuccessSeed covers the success arm of
// runCreateTorrent at create.go:225-257 including the andSeed
// branch that calls AddTorrentMetaInfo. We create a tiny file
// in a tempdir, run with andSeed=true, then join the async UI
// goroutine via the afterCreateTorrent seam (the seam fires after
// the fyne.Do callback returns, so AddTorrentMetaInfoSeedFrom and
// its render are covered too).
func TestRunCreateTorrentSuccessSeed(t *testing.T) {
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

	tmp := t.TempDir()
	src := filepath.Join(tmp, "payload.bin")
	if err := os.WriteFile(src, []byte("hello swartznet"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(tmp, "out.torrent")

	runCreateTorrent(d, w, engine.CreateTorrentOptions{
		Root: src,
		Name: "test",
	}, out, true)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCreateTorrent goroutine did not complete")
	}
}
