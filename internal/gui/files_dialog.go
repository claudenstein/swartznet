package gui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/engine"
)

// showFilesDialog opens a modal showing per-file progress and
// priority controls for the given torrent. Polls every 2 s while
// the dialog is open so progress bars update live.
func showFilesDialog(d *daemon.Daemon, win fyne.Window, infoHashHex, torrentName string) {
	files, err := d.Eng.TorrentFiles(infoHashHex)
	if err != nil {
		dialog.ShowError(err, win)
		return
	}
	if len(files) == 0 {
		dialog.ShowInformation("Waiting for metadata",
			"Torrent metadata has not arrived yet. Try again in a few seconds.", win)
		return
	}

	fd := &filesDialog{
		d:           d,
		win:         win,
		infoHashHex: infoHashHex,
		files:       files,
	}
	fd.build(torrentName)
}

type filesDialog struct {
	d           *daemon.Daemon
	win         fyne.Window
	infoHashHex string

	mu    sync.RWMutex
	files []engine.FileSnapshot

	list *widget.List
	dlg  dialog.Dialog
}

func (fd *filesDialog) build(torrentName string) {
	fd.list = widget.NewList(
		func() int {
			fd.mu.RLock()
			defer fd.mu.RUnlock()
			return len(fd.files)
		},
		func() fyne.CanvasObject {
			// Row layout: name | size | progress bar | priority select
			name := widget.NewLabel("file name")
			name.Truncation = fyne.TextTruncateEllipsis
			size := widget.NewLabel("size")
			progress := widget.NewProgressBar()
			prio := widget.NewSelect([]string{"none", "normal", "high"}, nil)
			prio.Resize(fyne.NewSize(100, 0))
			return container.NewBorder(
				nil, nil,
				nil,
				container.NewHBox(size, prio),
				container.NewVBox(name, progress),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			fd.mu.RLock()
			defer fd.mu.RUnlock()
			if id >= len(fd.files) {
				return
			}
			f := fd.files[id]
			border := obj.(*fyne.Container)
			leftCol := border.Objects[0].(*fyne.Container)  // Name+progress VBox
			rightCol := border.Objects[1].(*fyne.Container) // size+prio HBox
			nameLbl := leftCol.Objects[0].(*widget.Label)
			progress := leftCol.Objects[1].(*widget.ProgressBar)
			sizeLbl := rightCol.Objects[0].(*widget.Label)
			prioSelect := rightCol.Objects[1].(*widget.Select)

			nameLbl.SetText(f.DisplayPath)
			sizeLbl.SetText(humanBytes(f.Length))
			progress.SetValue(f.Progress)
			prioSelect.SetSelected(f.Priority)

			idx := f.Index
			prioSelect.OnChanged = func(selected string) {
				go func() {
					err := fd.d.Eng.SetFilePriority(fd.infoHashHex, idx, engine.FilePriority(selected))
					if err != nil {
						fyne.Do(func() {
							dialog.ShowError(err, fd.win)
						})
					}
				}()
			}
		},
	)

	// Bulk actions.
	allBtn := widget.NewButton("Select All", func() {
		fd.setAllPriorities(engine.FilePriorityNormal)
	})
	noneBtn := widget.NewButton("Deselect All", func() {
		fd.setAllPriorities(engine.FilePriorityNone)
	})
	bulk := container.NewHBox(allBtn, noneBtn)

	header := container.NewVBox(
		widget.NewLabelWithStyle("Files in "+torrentName,
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true}),
		bulk,
		widget.NewSeparator(),
	)

	content := container.NewBorder(header, nil, nil, nil, fd.list)
	content.Resize(fyne.NewSize(720, 520))

	ctx, cancel := context.WithCancel(context.Background())
	go fd.pollLoop(ctx)

	fd.dlg = dialog.NewCustom("Torrent Files", "Close", content, fd.win)
	fd.dlg.Resize(fyne.NewSize(760, 580))
	fd.dlg.SetOnClosed(func() { cancel() })
	fd.dlg.Show()
}

func (fd *filesDialog) pollLoop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			files, err := fd.d.Eng.TorrentFiles(fd.infoHashHex)
			if err != nil {
				continue
			}
			fyne.Do(func() {
				fd.mu.Lock()
				fd.files = files
				fd.mu.Unlock()
				fd.list.Refresh()
			})
		}
	}
}

func (fd *filesDialog) setAllPriorities(priority engine.FilePriority) {
	fd.mu.RLock()
	indices := make([]int, 0, len(fd.files))
	for _, f := range fd.files {
		indices = append(indices, f.Index)
	}
	fd.mu.RUnlock()
	go func() {
		var failed []string
		for _, idx := range indices {
			if err := fd.d.Eng.SetFilePriority(fd.infoHashHex, idx, priority); err != nil {
				failed = append(failed, fmt.Sprintf("#%d: %v", idx, err))
			}
		}
		if len(failed) > 0 {
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("some files failed: %v", failed), fd.win)
			})
		}
	}()
}
