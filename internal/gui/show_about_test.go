package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestShowAboutDialog covers App.showAbout at app.go:276-304
// against a real daemon. With an empty IdentityPath the engine
// has no identity, so pubKey stays empty and copyableValue
// suppresses the Copy button (covered by the existing
// copyableValue test). LocalPort returns a non-zero
// OS-assigned port. The daemon has no HTTP API, so apiAddr is
// "disabled".
func TestShowAboutDialog(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	a := &App{
		daemon:  d,
		win:     w,
		version: "test-version",
	}
	a.showAbout()
}
