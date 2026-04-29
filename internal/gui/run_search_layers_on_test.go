package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestRunSearchLocalLayerOn covers runSearch's doLocal && Index!=nil
// arm. newTestDaemon constructs a non-nil Bleve Index, so toggling
// localChk on takes runSearch into the spawned local-search
// goroutine; an empty index returns zero hits without error and
// the orchestrator's WaitGroup falls through to buildResults.
func TestRunSearchLocalLayerOn(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	st := newSearchTab(nil, d)
	st.localChk.SetChecked(true)
	st.swarmChk.SetChecked(false)
	st.dhtChk.SetChecked(false)
	st.queryEntry.SetText("anything")
	st.runSearch()

	time.Sleep(500 * time.Millisecond)
}

// TestRunSearchSwarmLayerOn covers runSearch's doSwarm &&
// SwarmSearch()!=nil arm. The swarm protocol is always non-nil
// after engine.New, so toggling swarmChk on enters the goroutine;
// with no peers the Query returns an empty response promptly.
func TestRunSearchSwarmLayerOn(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	st := newSearchTab(nil, d)
	st.localChk.SetChecked(false)
	st.swarmChk.SetChecked(true)
	st.dhtChk.SetChecked(false)
	st.queryEntry.SetText("anything")
	st.runSearch()

	// Swarm Query has a 2s internal timeout; sleep beyond it.
	time.Sleep(2500 * time.Millisecond)
}
