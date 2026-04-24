// TestMain primes Fyne's lazy-initialised DefaultTheme() before
// any test goroutine runs. Without this, parallel tests in this
// package that call into Fyne theme functions (directly via
// swartzTheme.Color, indirectly via copyableValue's icon lookup,
// etc.) race on Fyne's internal theme cache — a known upstream
// race in fyne.io/fyne/v2@v2.7.3:
//
//     fyne.io/fyne/v2/theme.DefaultTheme() has a "Read at X" /
//     "Previous write at X" pattern on the first two concurrent
//     callers. Once the theme is materialised the reads are
//     safe; the race window is strictly the lazy init.
//
// We eliminate that window by forcing the init to complete
// sequentially, then letting the rest of the test suite run
// under -race as before. Costs one theme instantiation — ~µs.

package gui

import (
	"os"
	"testing"

	"fyne.io/fyne/v2/theme"
)

func TestMain(m *testing.M) {
	// Materialise the default theme in a single goroutine so
	// all subsequent parallel calls see a stable pointer.
	// Reading Background() is sufficient to force the full
	// init chain (theme → icons → colour palette) — the exact
	// same path concurrent callers would otherwise race on.
	_ = theme.DefaultTheme()
	_ = theme.BackgroundColor()

	os.Exit(m.Run())
}
