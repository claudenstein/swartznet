package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestAppSelectTabDispatchesByName covers SelectTab's switch on
// each accepted tab name (downloads/search/status/companion/
// settings, both lowercase and capitalised). An App with a
// pre-populated tabs container is enough — no daemon needed.
func TestAppSelectTabDispatchesByName(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	tabs := container.NewAppTabs(
		container.NewTabItem("Downloads", widget.NewLabel("0")),
		container.NewTabItem("Search", widget.NewLabel("1")),
		container.NewTabItem("Status", widget.NewLabel("2")),
		container.NewTabItem("Companion", widget.NewLabel("3")),
		container.NewTabItem("Settings", widget.NewLabel("4")),
	)
	a := &App{tabs: tabs}

	cases := []struct {
		name string
		want int
	}{
		{"downloads", 0}, {"Downloads", 0},
		{"search", 1}, {"Search", 1},
		{"status", 2}, {"Status", 2},
		{"companion", 3}, {"Companion", 3},
		{"settings", 4}, {"Settings", 4},
		// Genuinely case-insensitive + whitespace-trimmed now: these
		// previously fell through the two-spelling switch as no-ops.
		{"DOWNLOADS", 0},
		{"  search  ", 1},
		{"\tStatus\n", 2},
		{"CoMpAnIoN", 3},
		{" SETTINGS ", 4},
	}
	for _, c := range cases {
		a.SelectTab(c.name)
		if got := tabs.SelectedIndex(); got != c.want {
			t.Errorf("SelectTab(%q) → index %d, want %d", c.name, got, c.want)
		}
	}

	// Unknown name should leave the selection where it was.
	a.SelectTab("unknown")
	if got := tabs.SelectedIndex(); got != 4 {
		t.Errorf("SelectTab('unknown') changed selection to %d, want 4 (unchanged)", got)
	}
}

// TestAppSelectTabNilTabsShortCircuits covers SelectTab's
// `if a.tabs == nil { return }` arm at app.go:393-395.
func TestAppSelectTabNilTabsShortCircuits(t *testing.T) {
	t.Parallel()
	a := &App{}
	a.SelectTab("downloads") // must not panic
}

// TestAppCleanupCallsCancel covers Cleanup's `a.cancel()` call.
// Construct an App with a real context and verify the context is
// canceled after Cleanup returns.
func TestAppCleanupCallsCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{cancel: cancel}
	a.Cleanup()
	select {
	case <-ctx.Done():
		// good
	default:
		t.Error("Cleanup did not cancel the context")
	}
}
