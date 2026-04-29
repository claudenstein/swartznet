package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestSettingsButtonTapsRunCallbacks covers the three button
// OnTapped closures in newSettingsTab (settings.go ~50-90):
// Save -> save(), Apply (rate) -> applyRateLimits(), Apply
// (queue) -> applyQueueSettings(). The settingsTab.content field
// is the Border container holding all three cards; we walk it
// for *widget.Button objects and tap each.
func TestSettingsButtonTapsRunCallbacks(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()

	d := newTestDaemon(t)
	st := newSettingsTab(d)
	w.SetContent(st.content)

	// Pre-populate sane values so the button callbacks don't
	// all bail out into ShowError dialogs (which would still be
	// safe but render extra text — no need).
	st.uploadEntry.SetText("0")
	st.downloadEntry.SetText("0")
	st.maxActiveEntry.SetText("0")

	// Walk the tab's content tree for buttons by text.
	var saveBtn, applyRate, applyQueue *widget.Button
	applyCount := 0
	for _, child := range test.LaidOutObjects(st.content) {
		if btn, ok := child.(*widget.Button); ok {
			switch btn.Text {
			case "Save":
				saveBtn = btn
			case "Apply":
				applyCount++
				if applyCount == 1 {
					applyRate = btn
				} else {
					applyQueue = btn
				}
			}
		}
	}

	if saveBtn != nil && saveBtn.OnTapped != nil {
		saveBtn.OnTapped()
	}
	if applyRate != nil && applyRate.OnTapped != nil {
		applyRate.OnTapped()
	}
	if applyQueue != nil && applyQueue.OnTapped != nil {
		applyQueue.OnTapped()
	}
}
