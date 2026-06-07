package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestRunSearchAllLayersOff covers runSearch's branch where every
// layer checkbox is unchecked. The orchestrator goroutine spawns
// no per-layer goroutines (each guarded by a checkbox), waits on
// an empty WaitGroup (instant), then fyne.Do's the buildResults
// closure which writes statusLbl + result box. We sleep
// generously so the goroutine completes before the test
// returns; we don't read st.statusLbl.Text from the test
// goroutine because that would race with the buildResults
// SetText.
func TestRunSearchAllLayersOff(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	wait := joinRunSearch(t)

	st := newSearchTab(nil, d)
	st.localChk.SetChecked(false)
	st.swarmChk.SetChecked(false)
	st.dhtChk.SetChecked(false)
	st.queryEntry.SetText("anything")
	st.runSearch()

	// All-layers-off path still spawns the orchestrator goroutine +
	// fyne.Do(buildResults); join it deterministically via the seam.
	wait()
}
