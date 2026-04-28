package gui

import (
	"testing"

	"fyne.io/fyne/v2/widget"
)

// TestRunSearchEmptyQueryShortCircuits covers the
// `if q == "" { return }` early-return at search.go:108-110.
// Empty and whitespace-only queries must exit before touching
// dl.d, so a daemon-less searchTab is sufficient. We only need
// the queryEntry widget — no other fields are referenced before
// the return.
func TestRunSearchEmptyQueryShortCircuits(t *testing.T) {
	t.Parallel()
	st := &searchTab{queryEntry: widget.NewEntry()}
	st.runSearch() // empty query → return

	st.queryEntry.SetText("   \t   ")
	st.runSearch() // whitespace-only → return after TrimSpace
}
