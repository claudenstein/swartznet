package gui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// joinRunSearch arms the afterRunSearch test seam and returns a wait
// func that blocks until runSearch's spawned goroutine (including its
// inline fyne.Do render under the test driver) has fully completed.
// This replaces time.Sleep so the async render is drained
// deterministically, keeping rendering single-threaded against Fyne's
// unsynchronized global font/SVG caches. The wait timeout is generous
// because runSearch's orchestrator goroutine is bounded by an internal
// 10s context (the DHT/swarm fan-out can run nearly that long when no
// peers answer).
func joinRunSearch(t *testing.T) func() {
	t.Helper()
	done := make(chan struct{}, 1)
	afterRunSearch = func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { afterRunSearch = nil })
	return func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("runSearch goroutine did not complete")
		}
	}
}

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

	wait := joinRunSearch(t)

	st := newSearchTab(nil, d)
	st.localChk.SetChecked(true)
	st.swarmChk.SetChecked(false)
	st.dhtChk.SetChecked(false)
	st.queryEntry.SetText("anything")
	st.runSearch()

	wait()
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

	wait := joinRunSearch(t)

	st := newSearchTab(nil, d)
	st.localChk.SetChecked(false)
	st.swarmChk.SetChecked(true)
	st.dhtChk.SetChecked(false)
	st.queryEntry.SetText("anything")
	st.runSearch()

	// Swarm Query has a 2s internal timeout; the seam fires once the
	// orchestrator goroutine's fyne.Do render returns.
	wait()
}
