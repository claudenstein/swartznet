package gui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/engine"
)

// downloadsTab holds the Downloads tab state.
type downloadsTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon

	mu    sync.RWMutex
	snaps []engine.TorrentSnapshot

	table    *widget.Table
	selected int // -1 = none
}

// Column definitions for the torrent table.
var dlColumns = []struct {
	name     string
	minWidth float32
}{
	{"Name", 250},
	{"Status", 90},
	{"Progress", 100},
	{"Size", 90},
	{"Peers", 60},
	{"Indexed", 70},
}

func newDownloadsTab(ctx context.Context, d *daemon.Daemon) *downloadsTab {
	dl := &downloadsTab{
		d:        d,
		selected: -1,
	}

	dl.table = widget.NewTableWithHeaders(
		// Length
		func() (rows int, cols int) {
			dl.mu.RLock()
			defer dl.mu.RUnlock()
			return len(dl.snaps), len(dlColumns)
		},
		// CreateCell
		func() fyne.CanvasObject {
			return widget.NewLabel("placeholder text here")
		},
		// UpdateCell
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			dl.mu.RLock()
			defer dl.mu.RUnlock()
			if id.Row >= len(dl.snaps) {
				label.SetText("")
				return
			}
			s := dl.snaps[id.Row]
			switch id.Col {
			case 0: // Name
				name := s.Name
				if name == "" {
					name = s.InfoHash[:16] + "..."
				}
				label.SetText(name)
			case 1: // Status
				label.SetText(s.Status)
			case 2: // Progress
				label.SetText(fmt.Sprintf("%.1f%%", s.Progress*100))
			case 3: // Size
				label.SetText(humanBytes(s.Size))
			case 4: // Peers
				label.SetText(fmt.Sprintf("%d", s.ActivePeers))
			case 5: // Indexed
				if s.Indexing {
					label.SetText("yes")
				} else {
					label.SetText("no")
				}
			}
		},
	)

	dl.table.CreateHeader = func() fyne.CanvasObject {
		return widget.NewLabel("Header")
	}
	dl.table.UpdateHeader = func(id widget.TableCellID, cell fyne.CanvasObject) {
		label := cell.(*widget.Label)
		if id.Row == -1 && id.Col >= 0 && id.Col < len(dlColumns) {
			label.TextStyle.Bold = true
			label.SetText(dlColumns[id.Col].name)
		}
	}

	for i, col := range dlColumns {
		dl.table.SetColumnWidth(i, col.minWidth)
	}

	dl.table.OnSelected = func(id widget.TableCellID) {
		dl.mu.Lock()
		dl.selected = id.Row
		dl.mu.Unlock()
	}

	// Action buttons.
	addMagnetBtn := widget.NewButtonWithIcon("Add Magnet", theme.ContentAddIcon(), func() {
		dl.showAddMagnetDialog()
	})
	addFileBtn := widget.NewButtonWithIcon("Add .torrent", theme.FolderOpenIcon(), func() {
		dl.showAddFileDialog()
	})
	createBtn := widget.NewButtonWithIcon("Create Torrent", theme.DocumentCreateIcon(), func() {
		createTorrentDialog(dl.d, dl.win())
	})
	pauseBtn := widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() {
		dl.pauseSelected()
	})
	resumeBtn := widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() {
		dl.resumeSelected()
	})
	removeBtn := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
		dl.removeSelected()
	})
	toggleIndexBtn := widget.NewButtonWithIcon("Toggle Index", theme.SearchIcon(), func() {
		dl.toggleIndexSelected()
	})
	filesBtn := widget.NewButtonWithIcon("Files...", theme.StorageIcon(), func() {
		dl.showFilesForSelected()
	})

	toolbar := container.NewHBox(
		addMagnetBtn,
		addFileBtn,
		createBtn,
		widget.NewSeparator(),
		pauseBtn,
		resumeBtn,
		removeBtn,
		widget.NewSeparator(),
		filesBtn,
		toggleIndexBtn,
	)

	// Wrap the table in a right-click capture so secondary taps
	// surface a context menu operating on the selected row.
	tableWithMenu := newRightClickCapture(dl.table, dl.buildContextMenu)

	dl.content = container.NewBorder(toolbar, nil, nil, nil, tableWithMenu)

	// Background polling goroutine.
	go dl.pollLoop(ctx)

	return dl
}

// buildContextMenu builds the right-click menu for the currently-
// selected torrent. Returns nil when no row is selected.
func (dl *downloadsTab) buildContextMenu() *fyne.Menu {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return nil
	}

	var snap engine.TorrentSnapshot
	dl.mu.RLock()
	for _, s := range dl.snaps {
		if s.InfoHash == ih {
			snap = s
			break
		}
	}
	dl.mu.RUnlock()

	pauseLabel := "Pause"
	pauseAction := func() { dl.pauseSelected() }
	if snap.Paused {
		pauseLabel = "Resume"
		pauseAction = func() { dl.resumeSelected() }
	}

	indexLabel := "Stop indexing"
	if !snap.Indexing {
		indexLabel = "Start indexing"
	}

	copyMagnet := fyne.NewMenuItem("Copy magnet link", func() {
		magnet := "magnet:?xt=urn:btih:" + ih
		if snap.Name != "" {
			magnet += "&dn=" + snap.Name
		}
		fyne.CurrentApp().Clipboard().SetContent(magnet)
	})
	copyHash := fyne.NewMenuItem("Copy infohash", func() {
		fyne.CurrentApp().Clipboard().SetContent(ih)
	})

	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Files...", func() { dl.showFilesForSelected() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(pauseLabel, pauseAction),
		fyne.NewMenuItem("Remove", func() { dl.removeSelected() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(indexLabel, func() { dl.toggleIndexSelected() }),
	}

	// Queue reorder actions — only surface them when this
	// torrent is currently queued (nothing to reorder otherwise).
	if snap.Queued {
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Move to top of queue", func() {
				go dl.d.Eng.QueueMoveToFront(ih)
			}),
			fyne.NewMenuItem("Move to bottom of queue", func() {
				go dl.d.Eng.QueueMoveToBack(ih)
			}),
		)
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		copyMagnet,
		copyHash,
	)
	return fyne.NewMenu("Torrent actions", items...)
}

func (dl *downloadsTab) pollLoop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			snaps := dl.d.Eng.TorrentSnapshots()
			fyne.Do(func() {
				dl.mu.Lock()
				dl.snaps = snaps
				dl.mu.Unlock()
				dl.table.Refresh()
			})
		}
	}
}

func (dl *downloadsTab) showAddMagnetDialog() {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("magnet:?xt=urn:btih:...")
	entry.MultiLine = false

	indexCheck := widget.NewCheck("Index this torrent's files after download", nil)
	indexCheck.SetChecked(true)

	d := dialog.NewForm(
		"Add Magnet URI",
		"Add",
		"Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Magnet URI", entry),
			widget.NewFormItem("", indexCheck),
		},
		func(ok bool) {
			if !ok || entry.Text == "" {
				return
			}
			uri := entry.Text
			shouldIndex := indexCheck.Checked
			go func() {
				ih, err := dl.d.Eng.AddMagnetURI(uri)
				if err != nil {
					fyne.Do(func() {
						dialog.ShowError(err, dl.win())
					})
					return
				}
				if !shouldIndex {
					// Flip the flag immediately so autoIndex's
					// 5-minute wait for metadata doesn't index it
					// when GotInfo fires.
					_ = dl.d.Eng.SetTorrentIndexing(ih, false)
				}
			}()
		},
		dl.win(),
	)
	d.Resize(fyne.NewSize(500, 180))
	d.Show()
}

func (dl *downloadsTab) showAddFileDialog() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		path := reader.URI().Path()
		reader.Close()
		go func() {
			h, err := dl.d.Eng.AddTorrentFile(path)
			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(err, dl.win())
				})
				return
			}
			// .torrent adds default to indexing = on; the user can
			// flip it via the Toggle Index button afterwards. We
			// don't prompt here because most .torrent adds are
			// existing collections the user wants searchable.
			_ = h
		}()
	}, dl.win())
	fd.SetFilter(&torrentFilter{})
	fd.Show()
}

func (dl *downloadsTab) showFilesForSelected() {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return
	}
	var name string
	dl.mu.RLock()
	for _, s := range dl.snaps {
		if s.InfoHash == ih {
			name = s.Name
			if name == "" {
				name = s.InfoHash[:16] + "..."
			}
			break
		}
	}
	dl.mu.RUnlock()
	showFilesDialog(dl.d, dl.win(), ih, name)
}

func (dl *downloadsTab) toggleIndexSelected() {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return
	}
	// Read current flag from snapshot under lock, then flip it.
	dl.mu.RLock()
	var current bool
	for _, s := range dl.snaps {
		if s.InfoHash == ih {
			current = s.Indexing
			break
		}
	}
	dl.mu.RUnlock()
	go dl.d.Eng.SetTorrentIndexing(ih, !current)
}

func (dl *downloadsTab) pauseSelected() {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return
	}
	go dl.d.Eng.PauseTorrent(ih)
}

func (dl *downloadsTab) resumeSelected() {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return
	}
	go dl.d.Eng.ResumeTorrent(ih)
}

func (dl *downloadsTab) removeSelected() {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return
	}
	go func() {
		_ = dl.d.Eng.RemoveTorrent(ih)
		fyne.Do(func() {
			dl.mu.Lock()
			dl.selected = -1
			dl.mu.Unlock()
		})
	}()
}

func (dl *downloadsTab) selectedInfoHash() string {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	if dl.selected < 0 || dl.selected >= len(dl.snaps) {
		return ""
	}
	return dl.snaps[dl.selected].InfoHash
}

// win returns the parent window for dialogs. It walks up from the
// content canvas object.
func (dl *downloadsTab) win() fyne.Window {
	c := fyne.CurrentApp().Driver().CanvasForObject(dl.content)
	if c == nil {
		// Fallback: return the first window from the app.
		for _, w := range fyne.CurrentApp().Driver().AllWindows() {
			return w
		}
		return nil
	}
	// The canvas doesn't directly expose the window, but all
	// current Fyne drivers associate one window per canvas.
	for _, w := range fyne.CurrentApp().Driver().AllWindows() {
		if w.Canvas() == c {
			return w
		}
	}
	return nil
}

// torrentFilter limits file dialogs to .torrent files.
type torrentFilter struct{}

func (f *torrentFilter) Matches(uri fyne.URI) bool {
	return uri.Extension() == ".torrent"
}

// humanBytes formats a byte count with binary prefixes.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
