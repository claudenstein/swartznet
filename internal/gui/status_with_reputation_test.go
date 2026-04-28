package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestStatusRefreshWithReputationEntries covers status.refresh's
// reputation-snapshot loop at status.go:295-308. We pre-record a
// few hits in the engine's tracker so tracker.Snapshot returns a
// non-empty slice, exercising the per-pubkey row build (including
// the >16 char pubkey truncation).
func TestStatusRefreshWithReputationEntries(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// Seed two pubkeys: one short (<16 chars, no truncation) and
	// one long (>16 chars, exercises the truncation arm).
	d.Eng.ReputationTracker().RecordReturned(reputation.PubKeyHex("short"), 1)
	d.Eng.ReputationTracker().RecordReturned(reputation.PubKeyHex(
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	), 5)
	d.Eng.ReputationTracker().RecordConfirmed(reputation.PubKeyHex(
		"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	))

	st := buildStatusTab(d)
	st.refresh()

	// Mount + resize so the rep-list UpdateCell callback runs
	// for each visible row.
	w.SetContent(st.content)
	w.Resize(fyne.NewSize(1200, 800))
	st.repList.Refresh()
}
