// Package gui implements the Fyne-based graphical user interface
// for SwartzNet. It calls internal packages directly — no HTTP
// round-trip through httpapi.
package gui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

// App holds the Fyne application, main window, and daemon reference.
type App struct {
	fyne    fyne.App
	win     fyne.Window
	daemon  *daemon.Daemon
	cancel  context.CancelFunc
	version string
	dl      *downloadsTab
	sr      *searchTab
	tabs    *container.AppTabs

	// lastNotified guards against re-sending the same completion
	// notification on every poll.
	lastNotified map[string]bool
}

// New creates the Fyne application and main window. Call Run to
// enter the event loop.
func New(d *daemon.Daemon, version string) *App {
	a := app.NewWithID("net.swartznet.gui")
	a.Settings().SetTheme(&swartzTheme{})
	a.SetIcon(AppIcon)

	win := a.NewWindow("SwartzNet " + version)
	win.SetIcon(AppIcon)
	win.Resize(fyne.NewSize(900, 600))

	ctx, cancel := context.WithCancel(context.Background())

	guiApp := &App{
		fyne:         a,
		win:          win,
		daemon:       d,
		cancel:       cancel,
		version:      version,
		lastNotified: make(map[string]bool),
	}

	dl := newDownloadsTab(ctx, d)
	sr := newSearchTab(ctx, d)
	st := newStatusTab(ctx, d)
	cp := newCompanionTab(ctx, d)
	se := newSettingsTab(d)
	guiApp.dl = dl
	guiApp.sr = sr

	tabs := container.NewAppTabs(
		container.NewTabItem("Downloads", dl.content),
		container.NewTabItem("Search", sr.content),
		container.NewTabItem("Status", st.content),
		container.NewTabItem("Companion", cp.content),
		container.NewTabItem("Settings", se.content),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	guiApp.tabs = tabs

	guiApp.installShortcuts()

	// Main menu: About + Quit.
	aboutItem := fyne.NewMenuItem("About SwartzNet", func() {
		guiApp.showAbout()
	})
	helpMenu := fyne.NewMenu("Help", aboutItem)
	win.SetMainMenu(fyne.NewMainMenu(helpMenu))

	win.SetContent(tabs)

	// System tray (desktop-only feature).
	guiApp.setupSystemTray()

	// Close intercept — minimize to tray instead of quit when
	// the tray is available; otherwise fall back to normal close.
	win.SetCloseIntercept(func() {
		if _, ok := a.(desktop.App); ok {
			win.Hide()
		} else {
			cancel()
			win.Close()
		}
	})

	// Watch engine for new completed files and fire OS notifications.
	go guiApp.notificationLoop(ctx)

	return guiApp
}

// installShortcuts wires up the keyboard shortcuts on the main
// window's canvas:
//
//	Ctrl+N / Cmd+N   — Add magnet dialog (opens on Downloads tab).
//	Ctrl+F / Cmd+F   — Switch to Search tab and focus the query
//	                   entry. Users can start typing immediately.
//	Ctrl+Q / Cmd+Q   — Quit the application.
//	Delete           — Remove the currently-selected torrent row.
//
// Fyne uses desktop.CustomShortcut for key+modifier combinations;
// plain key-only shortcuts like Delete go through Canvas.SetOnTypedKey.
func (a *App) installShortcuts() {
	canvas := a.win.Canvas()

	ctrl := fyne.KeyModifierControl
	// On macOS, Fyne's convention is that Super (Cmd) maps through
	// the same modifier constant on every platform when the user's
	// keyboard layout is macOS. Using Control is the widely-
	// compatible default; macOS users who want Cmd-based shortcuts
	// can be accommodated in a later pass if feedback warrants it.

	addMagnet := &desktop.CustomShortcut{
		KeyName:  fyne.KeyN,
		Modifier: ctrl,
	}
	canvas.AddShortcut(addMagnet, func(_ fyne.Shortcut) {
		a.tabs.SelectIndex(0) // Downloads tab
		if a.dl != nil {
			a.dl.showAddMagnetDialog()
		}
	})

	focusSearch := &desktop.CustomShortcut{
		KeyName:  fyne.KeyF,
		Modifier: ctrl,
	}
	canvas.AddShortcut(focusSearch, func(_ fyne.Shortcut) {
		a.tabs.SelectIndex(1) // Search tab
		if a.sr != nil && a.sr.queryEntry != nil {
			canvas.Focus(a.sr.queryEntry)
		}
	})

	quit := &desktop.CustomShortcut{
		KeyName:  fyne.KeyQ,
		Modifier: ctrl,
	}
	canvas.AddShortcut(quit, func(_ fyne.Shortcut) {
		a.cancel()
		a.fyne.Quit()
	})

	// Delete key on a selected downloads row — bare key, no modifier.
	// Use SetOnTypedKey so we catch it regardless of which widget
	// holds focus (the table's own Tapped handler doesn't surface
	// key events).
	canvas.SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeyDelete && a.tabs.SelectedIndex() == 0 && a.dl != nil {
			a.dl.removeSelected()
		}
	})
}

// setupSystemTray wires up the system tray menu if the platform
// supports it (desktop.App type assertion).
func (a *App) setupSystemTray() {
	desk, ok := a.fyne.(desktop.App)
	if !ok {
		return
	}

	showItem := fyne.NewMenuItem("Show SwartzNet", func() {
		a.win.Show()
		a.win.RequestFocus()
	})
	addMagnetItem := fyne.NewMenuItem("Add Magnet...", func() {
		a.win.Show()
		a.win.RequestFocus()
		if a.dl != nil {
			a.dl.showAddMagnetDialog()
		}
	})
	aboutItem := fyne.NewMenuItem("About", func() {
		a.win.Show()
		a.showAbout()
	})

	menu := fyne.NewMenu("SwartzNet",
		showItem,
		addMagnetItem,
		fyne.NewMenuItemSeparator(),
		aboutItem,
	)

	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(AppIcon)
	desk.SetSystemTrayWindow(a.win)
}

// showAbout presents a modal about dialog with version and
// identity information.
func (a *App) showAbout() {
	var pubKey string
	if id := a.daemon.Eng.Identity(); id != nil {
		pubKey = id.PublicKeyHex()
	}

	var port string
	if p := a.daemon.Eng.LocalPort(); p > 0 {
		port = fmt.Sprintf("%d", p)
	} else {
		port = "unknown"
	}

	apiAddr := "disabled"
	if a.daemon.API != nil {
		apiAddr = a.daemon.API.Addr()
	}

	content := widget.NewForm(
		widget.NewFormItem("Version", widget.NewLabel(a.version)),
		widget.NewFormItem("Identity", widget.NewLabel(pubKey)),
		widget.NewFormItem("BitTorrent port", widget.NewLabel(port)),
		widget.NewFormItem("HTTP API", widget.NewLabel(apiAddr)),
		widget.NewFormItem("License", widget.NewLabel("MPL 2.0 (engine) + MIT (SwartzNet code)")),
	)

	dialog.ShowCustom("About SwartzNet",
		"Close", content, a.win)
}

// notificationLoop polls the engine for newly-completed torrents
// and fires a desktop notification on completion. Runs until ctx
// is cancelled.
func (a *App) notificationLoop(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			snaps := a.daemon.Eng.TorrentSnapshots()
			for _, s := range snaps {
				if s.Status == "seeding" && !a.lastNotified[s.InfoHash] {
					a.lastNotified[s.InfoHash] = true
					a.fyne.SendNotification(&fyne.Notification{
						Title:   "Download complete",
						Content: s.Name,
					})
				}
			}
		}
	}
}

// Run enters the Fyne event loop. Blocks until the window is closed
// OR (when system tray is available) until ShowAndRun returns. The
// tray can keep the daemon alive even after the window closes.
func (a *App) Run() {
	a.win.ShowAndRun()
}

// Cleanup cancels background goroutines. Call after Run returns.
func (a *App) Cleanup() {
	a.cancel()
}
