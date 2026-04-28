package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// tapCardButtons walks the *widget.Card's Content (an HBox of
// confirm + flag buttons) and invokes each button's OnTapped
// closure so the callback bodies execute. Used by the make*HitCard
// tests to exercise the per-card Confirm/Flag callback closures.
func tapCardButtons(t *testing.T, card fyne.CanvasObject) {
	t.Helper()
	c, ok := card.(*widget.Card)
	if !ok {
		t.Fatalf("expected *widget.Card, got %T", card)
	}
	box, ok := c.Content.(*fyne.Container)
	if !ok {
		t.Fatalf("expected card.Content to be *fyne.Container, got %T", c.Content)
	}
	for _, obj := range box.Objects {
		if btn, ok := obj.(*widget.Button); ok && btn.OnTapped != nil {
			btn.OnTapped()
		}
	}
}

// TestMakeHitCardButtonsFire taps each of the Confirm/Flag
// buttons on every variant of the three hit-card builders so
// each per-card OnTapped closure runs (which in turn calls
// confirmHit / flagHit). Daemon-backed because confirmHit
// and flagHit dereference Eng getters.
func TestMakeHitCardButtonsFire(t *testing.T) {
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

	const ih = "0123456789abcdef0123456789abcdef01234567"

	tapCardButtons(t, st.makeLocalHitCard(indexer.SearchHit{
		Name: "ubuntu", InfoHash: ih, DocType: "torrent",
	}))
	tapCardButtons(t, st.makeSwarmHitCard(swarmsearch.MergedHit{
		Name: "alpine", InfoHash: ih,
	}))
	tapCardButtons(t, st.makeDHTHitCard(dhtindex.LookupHit{
		Name: "fedora", InfoHash: ih,
	}))
}
