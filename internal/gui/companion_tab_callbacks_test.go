package gui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestCompanionTabRefreshButtonAndUnfollow covers buildCompanionTab's
// refresh-button and follow-list closures (companion.go:63-65 and
// companion.go:88-104). After construction we walk the rendered
// content for the "Refresh Now" + "Unfollow" buttons and tap each.
// We pre-populate ct.follows with a row that has lastErr set so
// the `if f.lastErr != ""` stats-string append fires, and the
// list's update closure runs once when the list lays out.
func TestCompanionTabRefreshButtonAndUnfollow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()

	d := newTestDaemon(t)
	ct := buildCompanionTab(d)
	ct.follows = []followRow{
		{
			pubkey:   "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			label:    "errorpub",
			torrents: 1,
			content:  2,
			lastSync: "2024-01-01T00:00:00Z",
			lastErr:  "fetch failed",
		},
	}
	ct.followList.Refresh()
	w.SetContent(ct.content)

	for _, child := range test.LaidOutObjects(ct.content) {
		if btn, ok := child.(*widget.Button); ok {
			switch btn.Text {
			case "Refresh Now":
				if btn.OnTapped != nil {
					btn.OnTapped()
				}
			case "Unfollow":
				if btn.OnTapped != nil {
					btn.OnTapped()
				}
			}
		}
	}
}
