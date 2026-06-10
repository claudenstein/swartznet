package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestConfirmHitWithSources covers confirmHit's RecordConfirmed
// arm at search.go:344-351. We pre-seed the SourceTracker with a
// pubkey for our test infohash, so when confirmHit runs the
// `len(pks) > 0` branch fires and tracker.RecordConfirmed gets
// called.
func TestConfirmHitWithSources(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// Seed the source tracker directly.
	const ih = "0123456789abcdef0123456789abcdef01234567"
	d.Eng.SourceTracker().Record(ih, "publisher-pubkey-1")

	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}
	st.confirmHit(ih)
}

// TestFlagHitWithSources covers flagHit's `len(pks) > 0` early
// branch at search.go:362-365 — the function takes the seeded
// sources straight to RecordFlagged, skipping the
// fallback-snapshot branch. Then the sources.Forget call runs.
func TestFlagHitWithSources(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	const ih = "abababababababababababababababababababab"
	d.Eng.SourceTracker().Record(ih, "publisher-pubkey-A")
	d.Eng.SourceTracker().Record(ih, "publisher-pubkey-B")

	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}
	st.flagHit(ih)
}

// TestFlagHitNoSourcesDoesNotDemoteAll is the regression test for
// flagHit's old `len(pks) == 0` fallback, which demoted EVERY
// known indexer when a hit had no source attribution. That was
// attacker-weaponizable: seed unattributed spam, get it flagged
// once, and the whole reputation table craters — including
// trusted publishers. Flagging an unattributed infohash must now
// be a no-op for reputation: no tracker entry gains a flag.
func TestFlagHitNoSourcesDoesNotDemoteAll(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// Seed tracker entries so the old fallback would have had
	// victims to demote.
	d.Eng.ReputationTracker().RecordReturned("known-publisher", 1)
	d.Eng.ReputationTracker().RecordReturned("other-publisher", 1)

	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}
	// This infohash has no sources recorded — flagHit must show
	// the "no attribution" note and leave all reputations alone.
	st.flagHit("ffffffffffffffffffffffffffffffffffffffff")

	for _, e := range d.Eng.ReputationTracker().Snapshot() {
		if e.Counters.HitsFlagged != 0 {
			t.Fatalf("flagHit with no sources demoted %q (HitsFlagged=%d); fan-out fallback regressed",
				e.PubKey, e.Counters.HitsFlagged)
		}
	}
}
