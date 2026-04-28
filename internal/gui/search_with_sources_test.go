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

// TestFlagHitFallbackSnapshot covers flagHit's `len(pks) == 0`
// fallback at search.go:367-374. We pre-record reputation
// entries so tracker.Snapshot returns rows, but flag an
// infohash that has no sources — the function then iterates
// the snapshot to populate pks.
func TestFlagHitFallbackSnapshot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// Seed a tracker entry so Snapshot is non-empty.
	d.Eng.ReputationTracker().RecordReturned("known-publisher", 1)

	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}
	// This infohash has no sources recorded, so the empty-pks
	// fallback path fires.
	st.flagHit("ffffffffffffffffffffffffffffffffffffffff")
}
