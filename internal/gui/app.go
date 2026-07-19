package gui

import (
	"context"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"

	"github.com/swartznet/swartznet/internal/daemon"
)

// App is the Fyne GUI. It owns the window and the tab poll goroutines but NOT
// the Daemon's subsystems — every action flows through the shared Daemon.
type App struct {
	fyne      fyne.App
	win       fyne.Window
	daemon    *daemon.Daemon
	cancel    context.CancelFunc
	version   string
	buildDate string

	dl   *downloadsTab
	sr   *searchTab
	tabs *container.AppTabs
}

// New constructs the GUI over an already-wired Daemon. version/buildDate come
// from the single build-stamped source (the cmd shim). Call Run to show it.
func New(d *daemon.Daemon, version, buildDate string) *App {
	fa := app.NewWithID("net.swartznet.gui")
	fa.Settings().SetTheme(swartzTheme{})
	fa.SetIcon(AppIcon)

	win := fa.NewWindow("SwartzNet " + version)
	win.SetIcon(AppIcon)
	win.Resize(fyne.NewSize(900, 600))

	ctx, cancel := context.WithCancel(context.Background())
	a := &App{fyne: fa, win: win, daemon: d, cancel: cancel, version: version, buildDate: buildDate}

	a.dl = newDownloadsTab(ctx, d)
	a.sr = newSearchTab(ctx, d)
	st := newStatusTab(ctx, d)
	cp := newCompanionTab(ctx, d)
	se := newSettingsTab(d)

	// Wire each tab's window resolver so dialogs raise on the main window.
	winFn := func() fyne.Window { return a.win }
	a.dl.win = winFn
	cp.win = winFn
	se.win = winFn

	a.tabs = container.NewAppTabs(
		container.NewTabItem("Downloads", container.NewScroll(a.dl.content)),
		container.NewTabItem("Search", container.NewScroll(a.sr.content)),
		container.NewTabItem("Status", container.NewScroll(st.content)),
		container.NewTabItem("Companion", container.NewScroll(cp.content)),
		container.NewTabItem("Settings", container.NewScroll(se.content)),
	)
	a.tabs.SetTabLocation(container.TabLocationTop)

	a.installMenu()
	win.SetContent(a.tabs)
	win.SetCloseIntercept(a.onClose)
	return a
}

func (a *App) installMenu() {
	fileMenu := fyne.NewMenu("File",
		fyne.NewMenuItem("Add Magnet…", func() {
			a.tabs.SelectIndex(0)
			a.dl.showAddDialog()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { a.onClose() }),
	)
	helpMenu := fyne.NewMenu("Help",
		fyne.NewMenuItem("About SwartzNet", func() { ShowAbout(a.win, a.daemon, a.version, a.buildDate) }),
	)
	a.win.SetMainMenu(fyne.NewMainMenu(fileMenu, helpMenu))
}

// Run shows the window and blocks until it closes.
func (a *App) Run() {
	a.win.ShowAndRun()
}

// onClose cancels the GUI-local context (stopping the tab poll goroutines),
// closes the Daemon (single lifecycle — the GUI owns it), and quits Fyne.
func (a *App) onClose() {
	a.cancel()
	if a.daemon != nil {
		_ = a.daemon.Close()
	}
	a.fyne.Quit()
}
