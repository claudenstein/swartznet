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

	// Empty-state overlay. Shown when there are no torrents;
	// Hidden otherwise. Updated in pollLoop.
	emptyState *fyne.Container

	// Column sorting. sortCol is the column index currently used
	// for sorting (-1 = insertion order from the engine, which
	// is effectively FIFO by add-time). sortDesc toggles between
	// ascending and descending.
	sortCol  int
	sortDesc bool
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
	{"↓ speed", 90},
	{"↑ speed", 90},
	{"Indexed", 70},
	{"Signed", 100},
}

func newDownloadsTab(ctx context.Context, d *daemon.Daemon) *downloadsTab {
	dl := &downloadsTab{
		d:        d,
		selected: -1,
		sortCol:  -1, // no active sort — engine insertion order
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
			case 5: // Download rate
				label.SetText(rateStr(s.DownloadRate))
			case 6: // Upload rate
				label.SetText(rateStr(s.UploadRate))
			case 7: // Indexed
				if s.Indexing {
					label.SetText("yes")
				} else {
					label.SetText("no")
				}
			case 8: // Signed
				switch {
				case s.SignedBy == "":
					label.SetText("—")
				case s.TrustedPublisher:
					label.SetText("★ " + s.SignedBy[:8])
				default:
					label.SetText("✓ " + s.SignedBy[:8])
				}
			}
		},
	)

	dl.table.CreateHeader = func() fyne.CanvasObject {
		return widget.NewLabel("Header")
	}
	dl.table.UpdateHeader = func(id widget.TableCellID, cell fyne.CanvasObject) {
		label := cell.(*widget.Label)
		// Fyne's NewTableWithHeaders renders BOTH a column header
		// row (id.Row == -1) and a row header column (id.Col == -1).
		// Blank the row-header cell so it doesn't show the
		// CreateHeader placeholder "Header" text.
		if id.Col == -1 {
			label.TextStyle.Bold = false
			label.SetText("")
			return
		}
		if id.Row == -1 && id.Col >= 0 && id.Col < len(dlColumns) {
			label.TextStyle.Bold = true
			text := dlColumns[id.Col].name
			dl.mu.RLock()
			active := dl.sortCol == id.Col
			desc := dl.sortDesc
			dl.mu.RUnlock()
			if active {
				if desc {
					text += " ▼"
				} else {
					text += " ▲"
				}
			}
			label.SetText(text)
		}
	}

	for i, col := range dlColumns {
		dl.table.SetColumnWidth(i, col.minWidth)
	}

	dl.table.OnSelected = func(id widget.TableCellID) {
		if id.Row == -1 {
			// Header row click: toggle sort on this column.
			dl.toggleSort(id.Col)
			return
		}
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

	// Empty-state overlay: shown on top of the table when there
	// are no torrents yet. We use a Stack container; pollLoop
	// shows/hides the overlay based on snapshot count.
	emptyLabel := widget.NewLabelWithStyle(
		"No torrents yet",
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)
	emptyHint := widget.NewLabelWithStyle(
		"Add a magnet link, import a .torrent file, or create a new torrent from local content.",
		fyne.TextAlignCenter,
		fyne.TextStyle{},
	)
	dl.emptyState = container.NewCenter(container.NewVBox(emptyLabel, emptyHint))
	dl.emptyState.Hide()

	body := container.NewStack(tableWithMenu, dl.emptyState)
	dl.content = container.NewBorder(toolbar, nil, nil, nil, body)

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
	var signatureItems []*fyne.MenuItem
	if snap.SignedBy != "" {
		signer := snap.SignedBy // capture
		trusted := snap.TrustedPublisher
		signatureItems = append(signatureItems,
			fyne.NewMenuItem("Verify signature...", func() {
				dl.showSignatureDialog(snap)
			}),
		)
		if trusted {
			signatureItems = append(signatureItems,
				fyne.NewMenuItem("Revoke trust for this publisher", func() {
					if ts := dl.d.Eng.TrustStore(); ts != nil {
						_ = ts.Remove(signer)
					}
				}),
			)
		} else {
			signatureItems = append(signatureItems,
				fyne.NewMenuItem("Trust this publisher", func() {
					if ts := dl.d.Eng.TrustStore(); ts != nil {
						_ = ts.Add(signer, "")
					}
				}),
			)
		}
		signatureItems = append(signatureItems,
			fyne.NewMenuItem("Copy publisher pubkey", func() {
				fyne.CurrentApp().Clipboard().SetContent(signer)
			}),
		)
	}

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
	if len(signatureItems) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		items = append(items, signatureItems...)
	}
	return fyne.NewMenu("Torrent actions", items...)
}

// showSignatureDialog opens a modal detailing the signature
// info for the given torrent: full pubkey, trust status, label
// (if trusted), and the info-hash that was signed.
func (dl *downloadsTab) showSignatureDialog(snap engine.TorrentSnapshot) {
	if snap.SignedBy == "" {
		return
	}

	label := ""
	if ts := dl.d.Eng.TrustStore(); ts != nil {
		label = ts.Label(snap.SignedBy)
	}

	trustLabel := widget.NewLabel("untrusted")
	if snap.TrustedPublisher {
		trustLabel.SetText("✓ trusted")
		trustLabel.TextStyle.Bold = true
	}

	labelDisplay := label
	if labelDisplay == "" {
		labelDisplay = "—"
	}

	content := widget.NewForm(
		widget.NewFormItem("Torrent", widget.NewLabel(snap.Name)),
		widget.NewFormItem("InfoHash", widget.NewLabel(snap.InfoHash)),
		widget.NewFormItem("Publisher pubkey", widget.NewLabel(snap.SignedBy)),
		widget.NewFormItem("Trust status", trustLabel),
		widget.NewFormItem("Trust label", widget.NewLabel(labelDisplay)),
	)
	dialog.ShowCustom("Signature verified", "Close", content, dl.win())
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
				dl.sortSnapsLocked()
				dl.mu.Unlock()
				dl.table.Refresh()
				if len(snaps) == 0 {
					dl.emptyState.Show()
				} else {
					dl.emptyState.Hide()
				}
			})
		}
	}
}

// toggleSort cycles the sort state for the given column index:
//   - click a different column → switch to ascending on that column
//   - click the active column → switch to descending
//   - click a column that's already descending → clear sort
func (dl *downloadsTab) toggleSort(col int) {
	if col < 0 || col >= len(dlColumns) {
		return
	}
	dl.mu.Lock()
	defer dl.mu.Unlock()
	switch {
	case dl.sortCol != col:
		dl.sortCol = col
		dl.sortDesc = false
	case !dl.sortDesc:
		dl.sortDesc = true
	default:
		dl.sortCol = -1
		dl.sortDesc = false
	}
	dl.sortSnapsLocked()
	dl.table.Refresh()
}

// sortSnapsLocked sorts dl.snaps in place according to dl.sortCol
// and dl.sortDesc. Caller must hold dl.mu write-locked.
func (dl *downloadsTab) sortSnapsLocked() {
	if dl.sortCol < 0 {
		return
	}
	less := snapLess(dl.sortCol, dl.sortDesc)
	sortSnapsSlice(dl.snaps, less)
}

// snapLess returns the comparator for a given column + direction.
func snapLess(col int, desc bool) func(a, b engine.TorrentSnapshot) bool {
	base := func(a, b engine.TorrentSnapshot) bool {
		switch col {
		case 0: // Name
			return a.Name < b.Name
		case 1: // Status
			return a.Status < b.Status
		case 2: // Progress
			return a.Progress < b.Progress
		case 3: // Size
			return a.Size < b.Size
		case 4: // Peers
			return a.ActivePeers < b.ActivePeers
		case 5: // Download rate
			return a.DownloadRate < b.DownloadRate
		case 6: // Upload rate
			return a.UploadRate < b.UploadRate
		case 7: // Indexed
			if a.Indexing == b.Indexing {
				return a.Name < b.Name
			}
			return !a.Indexing && b.Indexing
		case 8: // Signed — signed torrents first when ascending
			if (a.SignedBy != "") == (b.SignedBy != "") {
				return a.SignedBy < b.SignedBy
			}
			return a.SignedBy != "" && b.SignedBy == ""
		}
		return false
	}
	if desc {
		return func(a, b engine.TorrentSnapshot) bool { return base(b, a) }
	}
	return base
}

// sortSnapsSlice sorts s in place using less. Stable insertion
// sort — torrent lists are small enough that the O(n²) upper
// bound doesn't matter.
func sortSnapsSlice(s []engine.TorrentSnapshot, less func(a, b engine.TorrentSnapshot) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
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

// rateStr formats a bytes/sec rate. Returns "—" for zero so an
// idle torrent doesn't show a distracting "0 B/s".
func rateStr(bps int64) string {
	if bps <= 0 {
		return "—"
	}
	return humanBytes(bps) + "/s"
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
