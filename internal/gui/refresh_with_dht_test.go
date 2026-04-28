package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestStatusRefreshWithDHT covers the DHT-enabled arms of
// status.refresh: Lookup non-nil (PPMI/known-indexers), Bootstrap
// non-nil (anchor/admitted/pending counts), and Publisher non-nil
// (keywords/hits stats). Requires newDHTTestDaemon so DHT, Lookup,
// Bootstrap, and Publisher are all wired up.
func TestStatusRefreshWithDHT(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.Eng.Lookup() == nil {
		t.Skip("daemon did not wire up Lookup (DHT may not be available)")
	}

	st := buildStatusTab(d)
	st.refresh()
}

// TestCompanionRefreshWithDHT covers companion.refresh's CompPub
// + CompSub non-nil arms at companion.go:161-198. With CompSub
// wired up but no follows, the inner loop is empty; with CompPub
// the publisher status block executes including the pubKey
// truncation arm.
func TestCompanionRefreshWithDHT(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompPub == nil || d.CompSub == nil {
		t.Skip("daemon did not wire up CompPub/CompSub")
	}

	ct := buildCompanionTab(d)
	ct.refresh()

	// Add a follow so the follows-loop iterates at least once,
	// covering the per-row branch.
	var pub [32]byte
	copy(pub[:], []byte("32-byte-test-pubkey-padding-aaaaa"))
	d.CompSub.Follow(pub, "test-publisher")
	ct.refresh()
}
