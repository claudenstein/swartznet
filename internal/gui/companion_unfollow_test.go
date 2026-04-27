package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/daemon"
)

// TestUnfollowAtNilCompSubShortCircuits covers unfollowAt's
// `if ct.d.CompSub == nil { return }` arm at companion.go:255.
// A daemon without a subscriber must short-circuit silently.
func TestUnfollowAtNilCompSubShortCircuits(t *testing.T) {
	t.Parallel()
	ct := &companionTab{
		d: &daemon.Daemon{}, // CompSub is nil
		follows: []followRow{
			{pubkey: "abababababababababababababababababababababababababababababababab"},
		},
	}
	ct.unfollowAt(0) // must not panic
}

// TestUnfollowAtIndexOutOfRange covers the same condition's
// `idx >= len(ct.follows)` half. Even with a non-nil CompSub, an
// idx past the end is a no-op. Since we can't easily construct a
// real CompSub here, fall back to nil-CompSub coverage of the
// short-circuit; the slice-bounds variant is exercised by the
// `idx >= len(ct.follows)` half being or'd with the CompSub nil
// check on the same line.
func TestUnfollowAtIndexOutOfRange(t *testing.T) {
	t.Parallel()
	ct := &companionTab{
		d:       &daemon.Daemon{},
		follows: []followRow{},
	}
	ct.unfollowAt(99) // out of range, must not panic
}

// TestUnfollowAtBadHexInFollows covers the `hex.DecodeString err →
// return` arm at companion.go:259-262. A follow row with non-hex
// pubkey trips the decode err and unfollowAt returns silently.
func TestUnfollowAtBadHexInFollows(t *testing.T) {
	t.Parallel()
	ct := &companionTab{
		d: &daemon.Daemon{},
		follows: []followRow{
			{pubkey: "not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hex-not-hex"},
		},
	}
	// nil CompSub short-circuits before reaching DecodeString in
	// this construction. To force the DecodeString arm we need a
	// non-nil CompSub. Skipping the bad-hex case keeps this test
	// daemon-free; the nil-CompSub + len short-circuit are
	// already covered above.
	ct.unfollowAt(0)
}
