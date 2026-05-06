package gui

import (
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// TestRunSearchEmptyQueryShortCircuits covers the
// `if q == "" { return }` early-return at search.go:108-110.
// Empty and whitespace-only queries must exit before touching
// dl.d, so a daemon-less searchTab is sufficient.
//
// Asserts the *observable side effects* of the early return:
// the searchBtn must stay enabled, the status label must stay
// empty, and the result box must keep whatever it already had.
// A regression that fell through into the search-firing block
// would call searchBtn.Disable() / statusLbl.SetText / RemoveAll
// before any goroutine launched, which is what we lock in here.
func TestRunSearchEmptyQueryShortCircuits(t *testing.T) {
	t.Parallel()
	st := &searchTab{
		queryEntry: widget.NewEntry(),
		searchBtn:  widget.NewButton("Search", nil),
		statusLbl:  widget.NewLabel(""),
		progress:   widget.NewProgressBarInfinite(),
		resultBox:  container.NewVBox(widget.NewLabel("preexisting")),
		emptyState: container.NewVBox(),
	}
	// Pre-disable progress so we can detect any spurious Show().
	st.progress.Hide()

	for _, q := range []string{"", "   \t   "} {
		st.queryEntry.SetText(q)
		st.runSearch()

		if st.searchBtn.Disabled() {
			t.Fatalf("runSearch(%q): searchBtn was disabled — early return broke", q)
		}
		if st.statusLbl.Text != "" {
			t.Fatalf("runSearch(%q): statusLbl=%q, want empty — early return broke", q, st.statusLbl.Text)
		}
		if len(st.resultBox.Objects) != 1 {
			t.Fatalf("runSearch(%q): resultBox lost preexisting content (%d objects) — early return broke", q, len(st.resultBox.Objects))
		}
	}
}
