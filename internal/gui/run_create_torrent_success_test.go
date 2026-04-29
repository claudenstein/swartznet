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
// in a tempdir, run with andSeed=true, then drain.
func TestRunCreateTorrentSuccessSeed(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

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

	// Drain: hashing tiny file is near-instant; AddTorrentMetaInfo
	// returns quickly too. 800ms covers both arms.
	time.Sleep(800 * time.Millisecond)
}
