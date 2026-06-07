package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestRunSearchDHTLayerOn covers runSearch's
// `doDHT && Lookup() != nil` arm. The DHT-enabled test daemon
// has a non-nil Lookup, so toggling dhtChk on enters the
// goroutine; the Lookup returns empty since no peers know the
// query, but the arm is exercised.
func TestRunSearchDHTLayerOn(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.Eng.Lookup() == nil {
		skipMissing(t, d, "Eng.Lookup")
		return
	}

	wait := joinRunSearch(t)

	st := newSearchTab(nil, d)
	st.localChk.SetChecked(false)
	st.swarmChk.SetChecked(false)
	st.dhtChk.SetChecked(true)
	st.queryEntry.SetText("anything")
	st.runSearch()

	// runSearch's outer goroutine has a 10s context timeout; the
	// inner Lookup fan-out times out at 2s with no peers. The seam
	// fires once that orchestrator goroutine's fyne.Do render returns.
	wait()
}
