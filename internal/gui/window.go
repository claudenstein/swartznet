package gui

import "fyne.io/fyne/v2"

// windowForObject returns the fyne.Window hosting the given
// canvas object, or nil if no such window is found in the
// current Fyne application's driver.
//
// It walks the current driver's AllWindows() list and matches on
// canvas identity. Fyne doesn't expose the parent window from a
// CanvasObject directly (by design — objects can technically be
// moved between canvases), but inside this GUI every object
// lives in exactly one window for its lifetime.
//
// This function exists so every tab doesn't have to reimplement
// the same "find my window" dance. Before its introduction the
// downloadsTab, searchTab, settingsTab, and companionTab each
// carried a near-identical copy that differed only in the
// receiver type.
//
// On an empty driver (no windows yet) returns nil.
func windowForObject(obj fyne.CanvasObject) fyne.Window {
	app := fyne.CurrentApp()
	if app == nil || obj == nil {
		return nil
	}
	canvas := app.Driver().CanvasForObject(obj)
	if canvas == nil {
		// Fall back to the first window the driver knows about.
		// This keeps dialogs reachable during early startup
		// (before the canvas is fully assembled) and in test
		// environments where CanvasForObject returns nil.
		for _, w := range app.Driver().AllWindows() {
			return w
		}
		return nil
	}
	for _, w := range app.Driver().AllWindows() {
		if w.Canvas() == canvas {
			return w
		}
	}
	return nil
}
