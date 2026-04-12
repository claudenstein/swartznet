package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// rightClickCapture wraps a CanvasObject so that right-clicks
// anywhere on it pop up a context menu. The menu is rebuilt on
// every right-click via the provided menuBuilder so actions can
// depend on the current selection / state.
//
// Fyne's widget.Table does not expose per-cell secondary tap
// events, so this wrapper uses "right-click on the downloads
// table after selecting a row" as its input gesture — functionally
// equivalent to a per-row context menu for our purposes.
type rightClickCapture struct {
	widget.BaseWidget
	child       fyne.CanvasObject
	menuBuilder func() *fyne.Menu
}

var _ fyne.SecondaryTappable = (*rightClickCapture)(nil)

func newRightClickCapture(child fyne.CanvasObject, menuBuilder func() *fyne.Menu) *rightClickCapture {
	r := &rightClickCapture{
		child:       child,
		menuBuilder: menuBuilder,
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *rightClickCapture) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.child)
}

func (r *rightClickCapture) TappedSecondary(ev *fyne.PointEvent) {
	if r.menuBuilder == nil {
		return
	}
	menu := r.menuBuilder()
	if menu == nil || len(menu.Items) == 0 {
		return
	}
	canvas := fyne.CurrentApp().Driver().CanvasForObject(r)
	if canvas == nil {
		return
	}
	widget.ShowPopUpMenuAtPosition(menu, canvas, ev.AbsolutePosition)
}
