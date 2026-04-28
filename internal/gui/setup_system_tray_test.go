package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

// TestSetupSystemTrayNonDesktopShortCircuits covers the
// `desk, ok := a.fyne.(desktop.App); if !ok { return }`
// arm at app.go:235-238. Fyne's test app is not a
// desktop.App, so the type assertion fails and the
// function exits before touching menu/icon APIs.
func TestSetupSystemTrayNonDesktopShortCircuits(t *testing.T) {
	t.Parallel()
	fa := test.NewApp()
	defer fa.Quit()

	a := &App{fyne: fa}
	a.setupSystemTray() // must return silently
}
