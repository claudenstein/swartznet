package gui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/engine"
)

const downloadsPollInterval = time.Second

// downloadsTab lists torrents (live progress/status/peers) and drives add /
// pause / resume / remove / indexing-toggle — every action through the engine.
type downloadsTab struct {
	content  fyne.CanvasObject
	d        *daemon.Daemon
	list     *widget.List
	snaps    []engine.TorrentSnapshot
	selected int
	win      func() fyne.Window // resolves the window for dialogs (nil-safe)
}

func newDownloadsTab(ctx context.Context, d *daemon.Daemon) *downloadsTab {
	dl := &downloadsTab{d: d, selected: -1}
	dl.list = widget.NewList(
		func() int { return len(dl.snaps) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil,
				widget.NewLabel("name"), widget.NewLabel("status"),
				widget.NewProgressBar())
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(dl.snaps) {
				return
			}
			s := dl.snaps[i]
			row := o.(*fyne.Container)
			name := row.Objects[1].(*widget.Label)
			status := row.Objects[2].(*widget.Label)
			bar := row.Objects[0].(*widget.ProgressBar)
			nm := s.Name
			if nm == "" {
				nm = short16(s.InfoHash)
			}
			name.SetText(nm)
			status.SetText(fmt.Sprintf("%s  %s/%s  peers=%d",
				s.Status, humanBytes(s.BytesCompleted), humanBytes(s.Size), s.ActivePeers))
			bar.SetValue(s.Progress)
		},
	)
	dl.list.OnSelected = func(id widget.ListItemID) { dl.selected = id }
	dl.list.OnUnselected = func(widget.ListItemID) { dl.selected = -1 }

	addBtn := widget.NewButtonWithIcon("Add magnet / .torrent", theme.ContentAddIcon(), dl.showAddDialog)
	pauseBtn := widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() { dl.act("pause") })
	resumeBtn := widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() { dl.act("resume") })
	removeBtn := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), dl.removeSelected)
	toolbar := container.NewHBox(addBtn, pauseBtn, resumeBtn, removeBtn)

	dl.content = container.NewBorder(toolbar, nil, nil, nil, dl.list)
	dl.refresh()
	go dl.pollLoop(ctx)
	return dl
}

func (dl *downloadsTab) pollLoop(ctx context.Context) {
	t := time.NewTicker(downloadsPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fyne.Do(dl.refresh)
		}
	}
}

func (dl *downloadsTab) refresh() {
	if dl.d.Eng == nil {
		return
	}
	dl.snaps = dl.d.Eng.TorrentSnapshots()
	dl.list.Refresh()
}

// selectedInfoHash returns the currently-selected torrent's infohash, or "".
func (dl *downloadsTab) selectedInfoHash() string {
	if dl.selected < 0 || dl.selected >= len(dl.snaps) {
		return ""
	}
	return dl.snaps[dl.selected].InfoHash
}

func (dl *downloadsTab) act(action string) {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return
	}
	var err error
	switch action {
	case "pause":
		err = dl.d.Eng.PauseTorrent(ih)
	case "resume":
		err = dl.d.Eng.ResumeTorrent(ih)
	}
	if err != nil {
		dl.showErr(err)
		return
	}
	dl.refresh()
}

func (dl *downloadsTab) removeSelected() {
	ih := dl.selectedInfoHash()
	if ih == "" {
		return
	}
	if err := dl.d.Eng.RemoveTorrent(ih); err != nil {
		dl.showErr(err)
		return
	}
	dl.selected = -1
	dl.refresh()
}

// showAddDialog prompts for a magnet URI or a .torrent path and adds it.
func (dl *downloadsTab) showAddDialog() {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("magnet:?xt=urn:btih:...  or  /path/to/file.torrent")
	form := dialog.NewForm("Add torrent", "Add", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Target", entry)},
		func(ok bool) {
			if !ok {
				return
			}
			target := strings.TrimSpace(entry.Text)
			if target == "" {
				return
			}
			go func() {
				var err error
				if strings.HasPrefix(target, "magnet:") {
					_, err = dl.d.Eng.AddMagnet(target)
				} else {
					_, err = dl.d.Eng.AddTorrentFile(target)
				}
				fyne.Do(func() {
					if err != nil {
						dl.showErr(err)
						return
					}
					dl.refresh()
				})
			}()
		}, dl.window())
	form.Resize(fyne.NewSize(560, 160))
	form.Show()
}

func (dl *downloadsTab) window() fyne.Window {
	if dl.win != nil {
		return dl.win()
	}
	return nil
}

func (dl *downloadsTab) showErr(err error) {
	if w := dl.window(); w != nil {
		dialog.ShowError(err, w)
	}
}
