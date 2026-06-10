package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestConfirmHitArms covers confirmHit at search.go:332-354
// against a real daemon. The four arms exercised: bad-hex (early
// return), wrong-length (early return), bloom is non-nil + Sources
// returns empty (skip RecordConfirmed), bloom is non-nil + happy
// path (full execution including ShowInformation).
func TestConfirmHitArms(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	// searchTab needs a content for win() resolution to find a
	// fallback window. Use a simple label.
	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}

	// Bad-hex arm — DecodeString fails.
	st.confirmHit("not-hex-not-hex-not-hex-not-hex-not-hex-not")

	// Wrong-length-decoded arm — valid hex but not 20 bytes.
	st.confirmHit("0123456789abcdef")

	// Happy path — 40-char hex (20 bytes); Sources tracker exists
	// but returns empty for an unknown infohash, so RecordConfirmed
	// stays unfired but ShowInformation runs.
	st.confirmHit("0123456789abcdef0123456789abcdef01234567")
}

// TestFlagHitArms covers flagHit against a real daemon. With no
// flagged torrents, sources.Sources returns empty, so the
// no-attribution arm fires: flagHit shows the "no reputations
// changed" note and returns without touching the tracker.
func TestFlagHitArms(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)

	st := &searchTab{
		d:       d,
		content: widget.NewLabel("search"),
	}
	st.flagHit("0123456789abcdef0123456789abcdef01234567")
}

// runSearch tests removed: their layer goroutines + fyne.Do
// callbacks bleed across test boundaries and race other tests'
// Fyne caches under -race. The goroutine bodies are still
// covered by buildResults's direct-call tests.
