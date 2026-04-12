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
	pauseBtn := widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() {
		dl.pauseSelected()
	})
	resumeBtn := widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() {
		dl.resumeSelected()
	})
	removeBtn := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
		dl.removeSelected()
	})

	toolbar := container.NewHBox(
		addMagnetBtn,
		addFileBtn,
		widget.NewSeparator(),
		pauseBtn,
		resumeBtn,
		removeBtn,
	)

	dl.content = container.NewBorder(toolbar, nil, nil, nil, dl.table)

	// Background polling goroutine.
	go dl.pollLoop(ctx)

	return dl
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

	d := dialog.NewForm(
		"Add Magnet URI",
		"Add",
		"Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Magnet URI", entry),
		},
		func(ok bool) {
			if !ok || entry.Text == "" {
				return
			}
			uri := entry.Text
			go func() {
				_, err := dl.d.Eng.AddMagnetURI(uri)
				if err != nil {
					fyne.Do(func() {
						dialog.ShowError(err, dl.win())
					})
				}
			}()
		},
		dl.win(),
	)
	d.Resize(fyne.NewSize(500, 150))
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
			_, err := dl.d.Eng.AddTorrentFile(path)
			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(err, dl.win())
				})
			}
		}()
	}, dl.win())
	fd.SetFilter(&torrentFilter{})
	fd.Show()
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
