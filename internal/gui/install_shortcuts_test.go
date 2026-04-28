package gui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestInstallShortcutsWiring covers installShortcuts at
// app.go:180-230. The function wires up Ctrl+N, Ctrl+F,
// Ctrl+Q and a Delete-key handler. None of the wired
// callbacks fire during installation, so a daemon-less
// App is sufficient — we only need win, tabs, fyne, and
// cancel populated for the closure captures.
func TestInstallShortcutsWiring(t *testing.T) {
	fa := test.NewApp()
	defer fa.Quit()
	w := fa.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	tabs := container.NewAppTabs(
		container.NewTabItem("Downloads", widget.NewLabel("0")),
		container.NewTabItem("Search", widget.NewLabel("1")),
	)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := &App{
		fyne:   fa,
		win:    w,
		tabs:   tabs,
		cancel: cancel,
		// dl, sr left nil — the shortcut closures handle nil safely.
	}
	a.installShortcuts()

	// Drive each registered shortcut so its handler closure runs.
	type shortcutTyper interface {
		TypedShortcut(fyne.Shortcut)
	}
	st, ok := w.Canvas().(shortcutTyper)
	if !ok {
		t.Skip("test canvas does not expose TypedShortcut")
	}
	ctrl := fyne.KeyModifierControl
	st.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyN, Modifier: ctrl})
	st.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: ctrl})
	st.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyQ, Modifier: ctrl})

	// SetOnTypedKey handler — invoke the Delete-key arm directly
	// via the canvas getter.
	if h := w.Canvas().OnTypedKey(); h != nil {
		h(&fyne.KeyEvent{Name: fyne.KeyDelete})
	}
}

// TestInstallShortcutsWithDlAndSr covers the `a.dl != nil` and
// `a.sr != nil && a.sr.queryEntry != nil` arms of the
// AddShortcut closures, plus the Delete-key handler's full-truthy
// branch. We populate dl and sr with hand-rolled tabs that have
// just enough state to satisfy the closure paths without
// requiring a full daemon.
func TestInstallShortcutsWithDlAndSr(t *testing.T) {
	fa := test.NewApp()
	defer fa.Quit()
	w := fa.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	tabs := container.NewAppTabs(
		container.NewTabItem("Downloads", widget.NewLabel("0")),
		container.NewTabItem("Search", widget.NewLabel("1")),
	)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := newTestDaemon(t)
	dl := &downloadsTab{
		d:       d,
		content: widget.NewLabel("downloads"),
	}
	sr := &searchTab{
		d:          d,
		queryEntry: widget.NewEntry(),
	}

	a := &App{
		fyne:   fa,
		win:    w,
		tabs:   tabs,
		cancel: cancel,
		dl:     dl,
		sr:     sr,
	}
	a.installShortcuts()

	type shortcutTyper interface {
		TypedShortcut(fyne.Shortcut)
	}
	st, ok := w.Canvas().(shortcutTyper)
	if !ok {
		t.Skip("test canvas does not expose TypedShortcut")
	}
	ctrl := fyne.KeyModifierControl
	// Ctrl+N → showAddMagnetDialog (a.dl != nil arm).
	st.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyN, Modifier: ctrl})
	// Ctrl+F → focus query entry (a.sr.queryEntry != nil arm).
	st.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF, Modifier: ctrl})

	// Delete key on Downloads tab with dl set → removeSelected.
	tabs.SelectIndex(0)
	if h := w.Canvas().OnTypedKey(); h != nil {
		h(&fyne.KeyEvent{Name: fyne.KeyDelete})
	}
}
