package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestNewCompanionTabWithDHT covers newCompanionTab's constructor
// arms that fire when CompPub + CompSub are wired. The follow-list
// CreateCell + UpdateCell callbacks build a row layout and read
// from ct.follows. Adding a follow before the test exercises the
// per-row cell update.
func TestNewCompanionTabWithDHT(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompSub == nil {
		t.Skip("daemon did not wire up CompSub")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pre-seed a follow so the follow-list UpdateCell callback has
	// work to do during the initial refresh.
	var pub [32]byte
	copy(pub[:], []byte("32-byte-test-pubkey-padding-bbbbbbb"))
	d.CompSub.Follow(pub, "pre-existing-publisher")

	// Add a long pubkey label too so the >16-char truncation arm
	// in the follow-list UpdateCell callback fires.
	var longPub [32]byte
	copy(longPub[:], []byte("longer-32byte-test-pubkey-padding"))
	d.CompSub.Follow(longPub, "long-pubkey-label")

	ct := newCompanionTab(ctx, d)
	if ct == nil {
		t.Fatal("expected companionTab")
	}

	// Drive refresh once more directly with the seeded rows visible.
	ct.refresh()

	// Mount + resize so the follow-list UpdateCell callback runs
	// for each visible row.
	w.SetContent(ct.content)
	w.Resize(fyne.NewSize(1200, 800))
	ct.followList.Refresh()
}
