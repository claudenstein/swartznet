package gui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestStatusPollLoopSync covers status.pollLoop's body
// (initial refresh + ctx.Done early-return) by calling it
// synchronously with a canceled context. The goroutine never
// spawns, so no fyne.Do bleed.
func TestStatusPollLoopSync(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	st := buildStatusTab(d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st.pollLoop(ctx)
}

// TestNewDownloadsTabWrapper covers newDownloadsTab's 3-line
// wrapper (buildDownloadsTab + go pollLoop). downloads.pollLoop
// (unlike status/companion) has NO initial refresh, so a canceled
// ctx makes the goroutine exit on the very first select without
// any fyne.Do call. Safe to call newDownloadsTab here.
func TestNewDownloadsTabWrapper(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl := newDownloadsTab(ctx, nil)
	if dl == nil {
		t.Fatal("expected non-nil downloadsTab")
	}
}

// TestCompanionPollLoopSync covers companion.pollLoop's body
// (initial refresh + ctx.Done early-return) by calling it
// synchronously with a canceled context.
func TestCompanionPollLoopSync(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ct := buildCompanionTab(d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ct.pollLoop(ctx)
}

// TestNewStatusTabWrapper covers newStatusTab's 3-line wrapper
// (buildStatusTab + go pollLoop). The pollLoop goroutine runs an
// initial refresh that calls fyne.Do; we drain the goroutine
// before the test returns so its callbacks don't bleed.
func TestNewStatusTabWrapper(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := newStatusTab(ctx, d)
	if st == nil {
		t.Fatal("expected non-nil statusTab")
	}
	time.Sleep(500 * time.Millisecond)
}

// TestNewCompanionTabWrapper covers newCompanionTab's 3-line
// wrapper. Same drain pattern.
func TestNewCompanionTabWrapper(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ct := newCompanionTab(ctx, d)
	if ct == nil {
		t.Fatal("expected non-nil companionTab")
	}
	time.Sleep(500 * time.Millisecond)
}
