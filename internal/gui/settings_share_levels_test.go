package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestSettingsLoadCurrentShareLevelArms covers loadCurrent's
// switch on caps.ShareLocal (case 0, 1, default for 2+). We set
// the engine's capabilities to each value and re-construct a
// settingsTab so loadCurrent reads it.
func TestSettingsLoadCurrentShareLevelArms(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	sw := d.Eng.SwarmSearch()
	if sw == nil {
		skipMissing(t, d, "Eng.SwarmSearch")
		return
	}

	for _, lvl := range []int{0, 1, 2} {
		sw.SetCapabilities(swarmsearch.Capabilities{ShareLocal: lvl})
		st := newSettingsTab(d) // calls loadCurrent inside
		if st == nil {
			t.Fatal("expected settingsTab")
		}
	}
}

// TestSettingsSaveShareLevelArms covers save()'s switch on
// st.shareRadio.Selected. We pick each shareLevels[*] option in
// turn and call save().
func TestSettingsSaveShareLevelArms(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	if d.Eng.SwarmSearch() == nil {
		skipMissing(t, d, "Eng.SwarmSearch")
		return
	}

	st := newSettingsTab(d)
	for _, lvl := range shareLevels {
		st.shareRadio.SetSelected(lvl)
		st.save()
	}
}
