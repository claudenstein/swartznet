package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestDownloadsTableUpdateCellArms drives the
// newDownloadsTab UpdateCell callback so each switch-case at
// downloads.go:87-122 fires (Name with empty + non-empty,
// Status, Progress, Size, Peers, rates, Indexed yes/no, Signed
// none/trusted/untrusted). To force the callback to run we
// mount the dl.content in a test window and resize it large
// enough that every column gets requested.
func TestDownloadsTableUpdateCellArms(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("downloads")
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl := newDownloadsTab(ctx, nil)

	// Populate with rows hitting every cell-formatting branch:
	//   row 0: empty Name (uses InfoHash[:16] truncation)
	//   row 1: SignedBy + TrustedPublisher
	//   row 2: SignedBy without Trusted
	//   row 3: signed-by empty + Indexing=false
	dl.snaps = []engine.TorrentSnapshot{
		{
			InfoHash:     "0123456789abcdef0123456789abcdef01234567",
			Name:         "",
			Status:       "downloading",
			Progress:     0.5,
			Size:         1024,
			ActivePeers:  3,
			DownloadRate: 100,
			UploadRate:   50,
			Indexing:     true,
		},
		{
			InfoHash:         "abcdef0123456789abcdef0123456789abcdef00",
			Name:             "trusted",
			Status:           "seeding",
			Progress:         1.0,
			Size:             2048,
			ActivePeers:      0,
			SignedBy:         "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			TrustedPublisher: true,
			Indexing:         true,
		},
		{
			InfoHash:         "abcdef0123456789abcdef0123456789abcdef01",
			Name:             "untrusted",
			Status:           "queued",
			SignedBy:         "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			TrustedPublisher: false,
		},
		{
			InfoHash: "abcdef0123456789abcdef0123456789abcdef02",
			Name:     "no-sig",
			Status:   "paused",
			Indexing: false,
		},
	}

	w.SetContent(dl.content)
	w.Resize(fyne.NewSize(1600, 800))
	dl.table.Refresh()

	// Set sortCol + sortDesc so the header arrow-indicator
	// arms fire on the next refresh.
	dl.sortCol = 0
	dl.sortDesc = false
	dl.table.Refresh()
	dl.sortDesc = true
	dl.table.Refresh()

	// Drive OnSelected: a header cell (Row=-1 hits the toggleSort
	// arm) and a body cell (Row=0 sets selected).
	dl.table.Select(widget.TableCellID{Row: -1, Col: 0})
	dl.table.Select(widget.TableCellID{Row: 0, Col: 0})
}
