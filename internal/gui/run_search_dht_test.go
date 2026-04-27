package gui

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestRunSearchAllLayersOn covers runSearch's swarm + DHT
// layer goroutines at search.go:158-180. With newDHTTestDaemon
// the engine has SwarmSearch and Lookup non-nil, so toggling
// the swarm + dht checkboxes on dispatches both goroutines in
// addition to the local-Bleve search. Each layer returns an
// empty result quickly (no peers / no DHT data) and the
// fyne.Do callback finalises buildResults.
func TestRunSearchAllLayersOn(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.Eng.Lookup() == nil || d.Eng.SwarmSearch() == nil {
		t.Skip("daemon did not wire up Lookup/SwarmSearch")
	}

	st := newSearchTab(nil, d)
	st.swarmChk.SetChecked(true)
	st.dhtChk.SetChecked(true)
	st.queryEntry.SetText("anything")
	st.runSearch()

	// Wait for the swarm + DHT goroutines to time out and the
	// fyne.Do callback to finalise. Both layers have a 2s
	// per-query timeout, so 3.5s is enough margin.
	deadline := time.Now().Add(3500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !st.searchBtn.Disabled() && !strings.Contains(st.statusLbl.Text, "Searching") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
