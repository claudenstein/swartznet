package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestApplyQueueSettingsInvalidInput covers applyQueueSettings'
// validation arm at settings.go:117-121. A non-numeric input
// trips strconv.Atoi err and the function calls dialog.ShowError
// + return before reaching st.d.Eng. Requires only a content
// label and an anchor window for st.win() to resolve.
func TestApplyQueueSettingsInvalidInput(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	st := &settingsTab{
		content:        widget.NewLabel("settings"),
		maxActiveEntry: widget.NewEntry(),
	}
	st.maxActiveEntry.SetText("not a number")
	st.applyQueueSettings()

	// Negative-number arm — Atoi parses but n < 0 trips the
	// `n < 0` half of the same condition.
	st.maxActiveEntry.SetText("-5")
	st.applyQueueSettings()
}

// TestApplyQueueSettingsZeroAndEmpty covers applyQueueSettings'
// `if s == ""` (empty input → s = "0") arm and `if n == 0`
// (label = "unlimited") arm at settings.go:114-128. Both are
// daemon-touching arms — they call SetMaxActiveDownloads — so
// the test requires a real daemon.
func TestApplyQueueSettingsZeroAndEmpty(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	st := newSettingsTab(d)

	// Empty input: TrimSpace returns "", trips the `s == ""`
	// fallback to "0", then `n == 0` triggers the "unlimited"
	// label arm.
	st.maxActiveEntry.SetText("")
	st.applyQueueSettings()

	// Explicit "0" — already triggers the "unlimited" arm; left
	// here as a redundant check that the empty-string handling
	// converges on the same path.
	st.maxActiveEntry.SetText("0")
	st.applyQueueSettings()
}

// TestApplyRateLimitsInvalidInput covers applyRateLimits'
// upload-err and download-err validation arms at
// settings.go:139-148. Each malformed input trips parseKiB and
// causes ShowError + return before any daemon call.
func TestApplyRateLimitsInvalidInput(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	// Upload err arm — bad upload, anything in download.
	stUp := &settingsTab{
		content:       widget.NewLabel("settings"),
		uploadEntry:   widget.NewEntry(),
		downloadEntry: widget.NewEntry(),
	}
	stUp.uploadEntry.SetText("abc")
	stUp.downloadEntry.SetText("100")
	stUp.applyRateLimits()

	// Download err arm — valid upload, bad download.
	stDown := &settingsTab{
		content:       widget.NewLabel("settings"),
		uploadEntry:   widget.NewEntry(),
		downloadEntry: widget.NewEntry(),
	}
	stDown.uploadEntry.SetText("100")
	stDown.downloadEntry.SetText("xyz")
	stDown.applyRateLimits()
}
