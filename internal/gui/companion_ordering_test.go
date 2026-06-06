package gui

import (
	"encoding/hex"
	"sort"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCompanionRefreshDeterministicOrder asserts that refresh()
// produces a stable, sorted follow-row order regardless of the
// non-deterministic map iteration order returned by
// CompSub.Following(). Rows must sort by label then pubkey. We
// run refresh() several times and require byte-identical row
// orderings every time — a regression that dropped the sort.Slice
// would (eventually) reshuffle and fail this.
func TestCompanionRefreshDeterministicOrder(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newDHTTestDaemon(t)
	if d.CompSub == nil {
		skipMissing(t, d, "CompSub")
		return
	}

	// Follow several publishers with labels chosen so the sorted
	// order (by label) differs from any insertion order.
	type pub struct {
		hex   string
		label string
	}
	pubs := []pub{
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "zeta"},
		{"1111111111111111111111111111111111111111111111111111111111111111", "alpha"},
		{"8888888888888888888888888888888888888888888888888888888888888888", "mike"},
		// Two with identical label to force the secondary pubkey sort.
		{"2222222222222222222222222222222222222222222222222222222222222222", "dup"},
		{"3333333333333333333333333333333333333333333333333333333333333333", "dup"},
	}
	for _, p := range pubs {
		raw, err := hex.DecodeString(p.hex)
		if err != nil {
			t.Fatalf("decode %s: %v", p.hex, err)
		}
		var key [32]byte
		copy(key[:], raw)
		d.CompSub.Follow(key, p.label)
	}

	// Expected order: by label asc, then pubkey asc.
	want := make([]followRow, len(pubs))
	for i, p := range pubs {
		want[i] = followRow{pubkey: p.hex, label: p.label}
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].label != want[j].label {
			return want[i].label < want[j].label
		}
		return want[i].pubkey < want[j].pubkey
	})

	// Run refresh() repeatedly; each run must yield the same order.
	for run := 0; run < 8; run++ {
		ct := buildCompanionTab(d)
		ct.refresh()
		if len(ct.follows) != len(want) {
			t.Fatalf("run %d: got %d follows, want %d", run, len(ct.follows), len(want))
		}
		for i := range want {
			if ct.follows[i].label != want[i].label || ct.follows[i].pubkey != want[i].pubkey {
				t.Fatalf("run %d row %d: got (%s,%s) want (%s,%s)",
					run, i,
					ct.follows[i].label, ct.follows[i].pubkey,
					want[i].label, want[i].pubkey)
			}
		}
	}
}

// TestCompanionSelectionResolvesByPubkeyAfterReshuffle proves the
// context-menu selection is keyed by pubkey, not row index: after
// the row order changes (as it does every refresh when the
// underlying map reshuffles), the previously-selected publisher
// still resolves to the SAME pubkey — so Unfollow / Copy act on
// the right (non-destructive) target.
func TestCompanionSelectionResolvesByPubkeyAfterReshuffle(t *testing.T) {
	t.Parallel()

	const target = "8888888888888888888888888888888888888888888888888888888888888888"

	ct := &companionTab{}

	// Initial order: target at index 0.
	ct.follows = []followRow{
		{pubkey: target, label: "mike"},
		{pubkey: "1111111111111111111111111111111111111111111111111111111111111111", label: "alpha"},
		{pubkey: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", label: "zeta"},
	}
	// Simulate OnSelected firing on row 0 (the target).
	ct.followSelectedKey = ct.follows[0].pubkey
	if got, ok := ct.selectedRow(); !ok || got.pubkey != target {
		t.Fatalf("before reshuffle: selectedRow = (%v,%v), want target", got.pubkey, ok)
	}

	// Now reshuffle: target moves to the last index. With the OLD
	// index-based selection this would resolve to "alpha" — a
	// different publisher. With pubkey keying it must still be the
	// target.
	ct.follows = []followRow{
		{pubkey: "1111111111111111111111111111111111111111111111111111111111111111", label: "alpha"},
		{pubkey: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", label: "zeta"},
		{pubkey: target, label: "mike"},
	}
	got, ok := ct.selectedRow()
	if !ok {
		t.Fatal("after reshuffle: selectedRow not found")
	}
	if got.pubkey != target {
		t.Fatalf("after reshuffle: selectedRow pubkey = %s, want %s (selection leaked to wrong publisher)", got.pubkey, target)
	}

	// If the selected publisher disappears (unfollowed elsewhere),
	// selection must resolve to not-found rather than aliasing
	// some other row.
	ct.follows = []followRow{
		{pubkey: "1111111111111111111111111111111111111111111111111111111111111111", label: "alpha"},
	}
	if _, ok := ct.selectedRow(); ok {
		t.Fatal("after target removed: selectedRow should be not-found")
	}
}
