package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestSearchTabSubmitCallback covers the queryEntry.OnSubmitted
// closure built in newSearchTab (search.go:51). The callback
// invokes runSearch; with all layers off the spawned
// orchestrator goroutine completes via the empty-WaitGroup
// path. We sleep 1500ms after the trigger so the goroutine's
// fyne.Do(buildResults) finishes before the test returns.
func TestSearchTabSubmitCallback(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	st := newSearchTab(nil, d)

	wait := joinRunSearch(t)

	st.localChk.SetChecked(false)
	st.swarmChk.SetChecked(false)
	st.dhtChk.SetChecked(false)
	st.queryEntry.SetText("submit")
	if st.queryEntry.OnSubmitted != nil {
		st.queryEntry.OnSubmitted("submit")
	}
	wait()
}

// TestSearchTabButtonCallback covers the searchBtn OnTapped
// closure built in newSearchTab (search.go:67). Same pattern
// but isolated into its own test so the prior render's fyne.Do
// can never race a follow-up SetText.
func TestSearchTabButtonCallback(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	st := newSearchTab(nil, d)

	wait := joinRunSearch(t)

	st.localChk.SetChecked(false)
	st.swarmChk.SetChecked(false)
	st.dhtChk.SetChecked(false)
	st.queryEntry.SetText("button")
	if st.searchBtn.OnTapped != nil {
		st.searchBtn.OnTapped()
	}
	wait()
}
