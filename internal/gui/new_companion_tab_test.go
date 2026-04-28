package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/daemon"
)

// TestNewCompanionTabConstructs covers newCompanionTab at
// companion.go:43-141. The constructor builds publisher
// status labels, follow list, follow form, and starts
// pollLoop in a goroutine. pollLoop's first action is
// refresh(), which dereferences ct.d.CompPub / CompSub —
// both nil-checked, so an empty daemon.Daemon{} is enough.
func TestNewCompanionTabConstructs(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	ct := buildCompanionTab(&daemon.Daemon{})
	if ct == nil {
		t.Fatal("expected non-nil companionTab")
	}
	if ct.content == nil {
		t.Error("expected content wired up")
	}
	if ct.followList == nil {
		t.Error("expected followList wired up")
	}
}
