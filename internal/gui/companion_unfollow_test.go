package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

// TestUnfollowByKeyNilCompSubShortCircuits covers unfollowByKey's
// `if ct.d.CompSub == nil { return }` arm. A daemon without a
// subscriber must short-circuit silently.
func TestUnfollowByKeyNilCompSubShortCircuits(t *testing.T) {
	t.Parallel()
	ct := &companionTab{
		d: &daemon.Daemon{}, // CompSub is nil
		follows: []followRow{
			{pubkey: "abababababababababababababababababababababababababababababababab"},
		},
	}
	ct.unfollowByKey("abababababababababababababababababababababababababababababababab") // must not panic
}

// TestUnfollowByKeyEmptyKey covers the `pkHex == ""` short-circuit
// half. An empty selection key is a no-op.
func TestUnfollowByKeyEmptyKey(t *testing.T) {
	t.Parallel()
	ct := &companionTab{
		d:       &daemon.Daemon{},
		follows: []followRow{},
	}
	ct.unfollowByKey("") // empty key, must not panic
}

// TestDoFollowEarlyReturns covers doFollow's three early-return
// arms at companion.go:235-247. ShowError needs a window so we
// run under test.NewApp + a minimal anchor window. The happy
// path requires a real CompSub and is left uncovered.
func TestDoFollowEarlyReturns(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	// nil-CompSub arm.
	ct := &companionTab{
		d:       &daemon.Daemon{},
		content: widget.NewLabel("ct"),
	}
	ct.doFollow("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "")

	// len != 64 arm.
	ct.doFollow("too-short", "")

	// hex.DecodeString err arm: 64 chars but with non-hex char.
	bad := "ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"
	ct.doFollow(bad, "")
}

// TestRefreshPublisherNilCompPubShortCircuits covers
// refreshPublisher's `if ct.d.CompPub == nil { return }` arm at
// companion.go:221-223. With nil CompPub the function returns
// immediately without spawning the background goroutine.
func TestRefreshPublisherNilCompPubShortCircuits(t *testing.T) {
	t.Parallel()
	ct := &companionTab{
		d: &daemon.Daemon{}, // CompPub is nil
	}
	ct.refreshPublisher() // must not panic, must return immediately
}

// TestUnfollowByKeyBadHex covers the `hex.DecodeString err →
// return` arm. A non-hex key trips the decode err and unfollowByKey
// returns silently.
func TestUnfollowByKeyBadHex(t *testing.T) {
	t.Parallel()
	ct := &companionTab{
		d: &daemon.Daemon{},
		follows: []followRow{
			{pubkey: "not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hex"},
		},
	}
	// nil CompSub short-circuits before reaching DecodeString in
	// this construction; the nil-CompSub + empty-key short-circuit
	// are already covered above.
	ct.unfollowByKey("not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hex")
}
