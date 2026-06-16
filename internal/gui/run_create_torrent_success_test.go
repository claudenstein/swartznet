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
		Name: "test", // rename: info.Name != basename(src) — must still seed
	}, out, true)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCreateTorrent goroutine did not complete")
	}

	// The renamed torrent must reach seeding (0 bytes missing), not
	// sit at downloading/0%. This is the regression guard for the
	// "Create Torrent shows 0%" bug: storage keys on the real
	// basename (payload.bin), not the renamed info.Name ("test").
	deadline := time.Now().Add(5 * time.Second)
	var seen engine.TorrentSnapshot
	seeding := false
	for time.Now().Before(deadline) {
		for _, s := range d.Eng.TorrentSnapshots() {
			seen = s
			if s.Size > 0 && s.BytesMissing == 0 {
				seeding = true
			}
		}
		if seeding {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !seeding {
		t.Fatalf("renamed torrent did not reach seeding; last snapshot: %+v", seen)
	}
}
