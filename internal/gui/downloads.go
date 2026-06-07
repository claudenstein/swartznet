package gui

import (
	"context"
	"fmt"
	"strings"
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

// afterRemoveSelected and afterAddMagnet, when non-nil, are invoked at the
// very end of the removeSelected / showAddMagnetDialogPrefilled goroutines
// respectively (after any fyne.Do render returns). They exist only so tests
// can deterministically join those async UI goroutines under the Fyne test
// driver, which runs fyne.Do callbacks inline on the calling goroutine. Both
// are nil in production, so real-app timing/behavior is unchanged.
var (
	afterRemoveSelected func()
	afterAddMagnet      func()
)

// downloadsTab holds the Downloads tab state.
type downloadsTab struct {
	content fyne.CanvasObject
	d       *daemon.Daemon

	mu    sync.RWMutex
	snaps []engine.TorrentSnapshot

	table    *widget.Table
	selected int // -1 = none — last-clicked row, drives the right-click menu

	// selectedSet is the multi-selection set keyed by infohash.
	// Bulk actions (pause/resume/remove/toggle-index) iterate
	// it; with one entry the behaviour is identical to the old
	// single-selection model. Keying by infohash (rather than
	// row index) keeps selection stable across re-sorts and the
	// 2 s polling refresh, which can otherwise renumber rows
	// underneath the user.
	selectedSet map[string]struct{}

	// Empty-state overlay. Shown when there are no torrents;
	// Hidden otherwise. Updated in pollLoop.
	emptyState *fyne.Container

	// Column sorting. sortCol is the column index currently used
	// for sorting (-1 = insertion order from the engine, which
	// is effectively FIFO by add-time). sortDesc toggles between
	// ascending and descending.
	sortCol  int
	sortDesc bool

	// selectionLbl shows "(N selected)" in the toolbar so the
	// user can see at a glance how many rows the bulk buttons
	// will hit.
	selectionLbl *widget.Label
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
	dl := buildDownloadsTab(d)
	go dl.pollLoop(ctx)
	return dl
}

// buildDownloadsTab constructs the downloadsTab struct without
// spawning the pollLoop goroutine. Tests use it to avoid the
// goroutine racing test-thread widget operations under -race.
func buildDownloadsTab(d *daemon.Daemon) *downloadsTab {
	dl := &downloadsTab{
		d:           d,
		selected:    -1,
		sortCol:     -1, // no active sort — engine insertion order
		selectedSet: make(map[string]struct{}),
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
			// Ellipsis-truncate so a long Name doesn't render past
			// its column width and overprint the next column. The
			// table widget clips at column edges visually but the
			// label itself measures and paints at full text width
			// in some Fyne versions, producing the overlap seen in
			// scree.png.
			lbl := widget.NewLabel("placeholder text here")
			lbl.Truncation = fyne.TextTruncateEllipsis
			return lbl
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
				if _, ok := dl.selectedSet[s.InfoHash]; ok {
					name = "✓ " + name
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
		// Toggle multi-selection on each row tap. Standard
		// table widgets gate this behind Ctrl/Shift, but Fyne
		// does not surface modifier state through OnSelected;
		// rather than ship a half-working modifier hack, treat
		// every row click as a toggle and pair it with explicit
		// "Select All" / "Clear Selection" toolbar actions.
		// This makes the multi-select model discoverable and
		// keeps the door open to a true Ctrl-aware variant later.
		if id.Row >= 0 && id.Row < len(dl.snaps) {
			ih := dl.snaps[id.Row].InfoHash
			if _, ok := dl.selectedSet[ih]; ok {
				delete(dl.selectedSet, ih)
			} else {
				dl.selectedSet[ih] = struct{}{}
			}
		}
		dl.mu.Unlock()
		dl.refreshSelectionLabel()
		dl.table.Refresh()
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
	selectAllBtn := widget.NewButtonWithIcon("Select All", theme.ContentCopyIcon(), func() {
		dl.selectAll()
	})
	clearSelBtn := widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), func() {
		dl.clearSelection()
	})

	dl.selectionLbl = widget.NewLabel("")
	dl.selectionLbl.TextStyle.Italic = true

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
		widget.NewSeparator(),
		selectAllBtn,
		clearSelBtn,
		dl.selectionLbl,
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

	return dl
}

// buildContextMenu builds the right-click menu for the currently-
// selected torrent. Returns nil when no row is selected. When the
// multi-selection set has 2+ entries every action label is
// pluralised so the user can confirm the bulk operation visually
// before committing.
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
	bulkN := len(dl.selectedSet)
	dl.mu.RUnlock()

	bulkSuffix := ""
	if bulkN >= 2 {
		bulkSuffix = fmt.Sprintf(" (%d selected)", bulkN)
	}

	pauseLabel := "Pause" + bulkSuffix
	pauseAction := func() { dl.pauseSelected() }
	if snap.Paused {
		pauseLabel = "Resume" + bulkSuffix
		pauseAction = func() { dl.resumeSelected() }
	}

	indexLabel := "Stop indexing" + bulkSuffix
	if !snap.Indexing {
		indexLabel = "Start indexing" + bulkSuffix
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
		fyne.NewMenuItem("Remove"+bulkSuffix, func() { dl.removeSelected() }),
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
	dl.showAddMagnetDialogPrefilled("", true)
}

// showAddMagnetDialogPrefilled opens the Add Magnet dialog with
// the URI entry pre-populated and the "Index" checkbox
// initialised to indexChecked. Used both for the first-time
// "paste a magnet" path (empty prefill) and for the retry-after-
// error path (prefill = the bad URI) so users can fix a typo
// without re-pasting the whole string.
func (dl *downloadsTab) showAddMagnetDialogPrefilled(prefill string, indexChecked bool) {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("magnet:?xt=urn:btih:...")
	entry.MultiLine = false
	if prefill != "" {
		entry.SetText(prefill)
	}

	indexCheck := widget.NewCheck("Index this torrent's files after download", nil)
	indexCheck.SetChecked(indexChecked)

	// Build-and-reopen helper. If validation or the engine
	// rejects the submitted URI, we close this dialog, show the
	// error, and call showAddMagnetDialogPrefilled again with
	// the same URI + checkbox state — so the user can edit the
	// existing text instead of re-pasting.
	var d dialog.Dialog
	submit := func(ok bool) {
		if !ok {
			return
		}
		uri := strings.TrimSpace(entry.Text)
		shouldIndex := indexCheck.Checked
		if uri == "" {
			showAddMagnetError(dl, "paste a magnet URI starting with \"magnet:?xt=urn:btih:\"", uri, shouldIndex)
			return
		}
		// Shallow client-side validation so the user doesn't
		// have to wait for the engine to return a cryptic
		// error for an obviously-malformed paste.
		if reason := validateMagnetURI(uri); reason != "" {
			showAddMagnetError(dl, reason, uri, shouldIndex)
			return
		}
		go func() {
			// Signal the test seam (nil in production) as the very last
			// action of this goroutine, after any fyne.Do error render
			// has returned, so tests can join it deterministically. The
			// Fyne test driver runs fyne.Do callbacks inline+synchronously,
			// so by the time this defer fires the async render is done.
			defer func() {
				if afterAddMagnet != nil {
					afterAddMagnet()
				}
			}()
			ih, err := dl.d.Eng.AddMagnetURI(uri)
			if err != nil {
				fyne.Do(func() {
					showAddMagnetError(dl,
						"Could not add this magnet: "+friendlyAddErr(err),
						uri, shouldIndex,
					)
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
	}

	d = dialog.NewForm(
		"Add Magnet URI",
		"Add",
		"Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Magnet URI", entry),
			widget.NewFormItem("", indexCheck),
		},
		submit,
		dl.win(),
	)
	d.Resize(fyne.NewSize(500, 180))
	d.Show()
}

// showAddMagnetError presents an error dialog AND, on dismiss,
// reopens the Add Magnet dialog with the bad URI pre-filled so
// the user can edit it instead of starting over. The magnet URI
// is long enough that re-pasting it would be a real annoyance,
// and the common cause of errors (wrong scheme, missing btih) is
// a one- or two-character fix.
func showAddMagnetError(dl *downloadsTab, msg, uri string, indexChecked bool) {
	info := dialog.NewInformation("Add Magnet failed", msg, dl.win())
	info.SetOnClosed(func() {
		dl.showAddMagnetDialogPrefilled(uri, indexChecked)
	})
	info.Show()
}

// validateMagnetURI runs cheap checks on a pasted magnet string so
// we can fail fast with a friendly message instead of surfacing
// anacrolix's internal error text. Returns an empty string when
// the URI looks acceptable; otherwise the user-facing reason.
func validateMagnetURI(uri string) string {
	if !strings.HasPrefix(uri, "magnet:?") {
		return "magnet URI must start with \"magnet:?\" — did you paste a regular URL?"
	}
	if !strings.Contains(uri, "xt=urn:btih:") {
		return "magnet URI is missing the \"xt=urn:btih:\" infohash parameter"
	}
	return ""
}

// friendlyAddErr rewrites the most common engine error strings
// into user-facing phrasing. Unknown errors pass through.
func friendlyAddErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "zero infohash"):
		return "the magnet URI's infohash is all zeros — it needs a real 40-character btih value"
	case strings.Contains(msg, "parse magnet"):
		return "the magnet URI is malformed and couldn't be parsed"
	case strings.Contains(msg, "closed"):
		return "the engine is shutting down; try again after restart"
	default:
		return msg
	}
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
	targets := dl.actionTargets()
	if len(targets) == 0 {
		return
	}
	// Build a (infohash → currently-indexing?) map under lock so
	// the bulk operation flips each torrent independently.
	dl.mu.RLock()
	state := make(map[string]bool, len(targets))
	for _, ih := range targets {
		for _, s := range dl.snaps {
			if s.InfoHash == ih {
				state[ih] = s.Indexing
				break
			}
		}
	}
	dl.mu.RUnlock()
	go func() {
		for _, ih := range targets {
			_ = dl.d.Eng.SetTorrentIndexing(ih, !state[ih])
		}
	}()
}

func (dl *downloadsTab) pauseSelected() {
	targets := dl.actionTargets()
	if len(targets) == 0 {
		return
	}
	go func() {
		for _, ih := range targets {
			_ = dl.d.Eng.PauseTorrent(ih)
		}
	}()
}

func (dl *downloadsTab) resumeSelected() {
	targets := dl.actionTargets()
	if len(targets) == 0 {
		return
	}
	go func() {
		for _, ih := range targets {
			_ = dl.d.Eng.ResumeTorrent(ih)
		}
	}()
}

func (dl *downloadsTab) removeSelected() {
	targets := dl.actionTargets()
	if len(targets) == 0 {
		return
	}

	// Remove drops the torrent from the download list and stops
	// seeding/leeching it. engine.RemoveTorrent calls t.Drop()
	// under the hood, which does NOT delete on-disk files and
	// does NOT purge the Bleve content docs — those stay on disk
	// and in the index until the user removes them manually. The
	// confirm dialog exists mainly so the Delete key doesn't
	// silently vanish a row when the user meant to press a
	// different key.
	var promptBody string
	if len(targets) == 1 {
		ih := targets[0]
		var name string
		dl.mu.RLock()
		for _, s := range dl.snaps {
			if s.InfoHash == ih {
				name = s.Name
				break
			}
		}
		dl.mu.RUnlock()
		label := name
		if label == "" {
			label = ih[:16] + "..."
		}
		promptBody = fmt.Sprintf("Remove \"%s\" from the download list and stop seeding/leeching?\n\nDownloaded files on disk are kept; the torrent entry in your list is removed.", label)
	} else {
		promptBody = fmt.Sprintf("Remove %d torrents from the download list and stop seeding/leeching?\n\nDownloaded files on disk are kept; the torrent entries in your list are removed.", len(targets))
	}

	dialog.ShowConfirm(
		"Remove torrent?",
		promptBody,
		func(ok bool) {
			if !ok {
				return
			}
			go func() {
				for _, ih := range targets {
					_ = dl.d.Eng.RemoveTorrent(ih)
				}
				fyne.Do(func() {
					dl.mu.Lock()
					dl.selected = -1
					for _, ih := range targets {
						delete(dl.selectedSet, ih)
					}
					dl.mu.Unlock()
					dl.refreshSelectionLabel()
				})
				// fyne.Do under the test driver runs its callback
				// inline+synchronously, so by here the async render has
				// finished. Signal the test seam (nil in production) so
				// tests can join this goroutine deterministically.
				if afterRemoveSelected != nil {
					afterRemoveSelected()
				}
			}()
		},
		dl.win(),
	)
}

// actionTargets returns the list of infohashes a bulk action
// should hit: the multi-selection set when non-empty, otherwise
// the single primary selection (last clicked row). This keeps
// the existing toolbar / context-menu callers working on a
// fresh row without the user having to pre-select it.
func (dl *downloadsTab) actionTargets() []string {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	if len(dl.selectedSet) > 0 {
		// Walk snaps so the result is in display order — bulk
		// pause/resume looks tidier when row 1 fires before
		// row 2.
		out := make([]string, 0, len(dl.selectedSet))
		for _, s := range dl.snaps {
			if _, ok := dl.selectedSet[s.InfoHash]; ok {
				out = append(out, s.InfoHash)
			}
		}
		return out
	}
	if dl.selected < 0 || dl.selected >= len(dl.snaps) {
		return nil
	}
	return []string{dl.snaps[dl.selected].InfoHash}
}

// selectAll adds every currently-displayed torrent to the
// multi-selection set.
func (dl *downloadsTab) selectAll() {
	dl.mu.Lock()
	for _, s := range dl.snaps {
		dl.selectedSet[s.InfoHash] = struct{}{}
	}
	dl.mu.Unlock()
	dl.refreshSelectionLabel()
	dl.table.Refresh()
}

// clearSelection drops every entry from the multi-selection set.
func (dl *downloadsTab) clearSelection() {
	dl.mu.Lock()
	dl.selectedSet = make(map[string]struct{})
	dl.mu.Unlock()
	dl.refreshSelectionLabel()
	dl.table.Refresh()
}

// refreshSelectionLabel updates the toolbar count label.
// Called from any code that mutates dl.selectedSet so the user
// always sees an accurate selection count next to the bulk
// action buttons.
func (dl *downloadsTab) refreshSelectionLabel() {
	dl.mu.RLock()
	n := len(dl.selectedSet)
	dl.mu.RUnlock()
	if dl.selectionLbl == nil {
		return
	}
	if n == 0 {
		dl.selectionLbl.SetText("")
	} else {
		dl.selectionLbl.SetText(fmt.Sprintf("(%d selected)", n))
	}
}

func (dl *downloadsTab) selectedInfoHash() string {
	dl.mu.RLock()
	defer dl.mu.RUnlock()
	if dl.selected < 0 || dl.selected >= len(dl.snaps) {
		return ""
	}
	return dl.snaps[dl.selected].InfoHash
}

// win returns the parent window for dialogs.
func (dl *downloadsTab) win() fyne.Window { return windowForObject(dl.content) }

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
