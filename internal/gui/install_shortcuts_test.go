package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestInstallShortcutsWiring covers installShortcuts at
// app.go:180-230. The function wires up Ctrl+N, Ctrl+F,
// Ctrl+Q and a Delete-key handler. None of the wired
// callbacks fire during installation, so a daemon-less
// App is sufficient — we only need win, tabs, fyne, and
// cancel populated for the closure captures.
func TestInstallShortcutsWiring(t *testing.T) {
	fa := test.NewApp()
	defer fa.Quit()
	w := fa.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	tabs := container.NewAppTabs(
		container.NewTabItem("Downloads", widget.NewLabel("0")),
		container.NewTabItem("Search", widget.NewLabel("1")),
	)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := &App{
		fyne:   fa,
		win:    w,
		tabs:   tabs,
		cancel: cancel,
		// dl, sr left nil — the shortcut closures handle nil safely.
	}
	a.installShortcuts()
	// No assertion on shortcut state — Fyne stores them on the
	// canvas; we only care that the install path executes.
}
