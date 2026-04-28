package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestTabWinWrappers covers the four trivial `win()` wrappers
// (companionTab, downloadsTab, searchTab, settingsTab) that
// each return windowForObject(t.content). With no canvas
// attachment, they all fall back to the first known window.
func TestTabWinWrappers(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("test")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	// companionTab.
	ct := &companionTab{content: widget.NewLabel("ct")}
	if got := ct.win(); got == nil {
		t.Error("companionTab.win() = nil, want fallback window")
	}

	// downloadsTab.
	dl := &downloadsTab{content: widget.NewLabel("dl")}
	if got := dl.win(); got == nil {
		t.Error("downloadsTab.win() = nil, want fallback window")
	}

	// searchTab.
	st := &searchTab{content: widget.NewLabel("st")}
	if got := st.win(); got == nil {
		t.Error("searchTab.win() = nil, want fallback window")
	}

	// settingsTab.
	se := &settingsTab{content: widget.NewLabel("se")}
	if got := se.win(); got == nil {
		t.Error("settingsTab.win() = nil, want fallback window")
	}
}
