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
//
// Asserts that the initial refresh actually ran by checking
// that the torrents-card labels carry their refreshed text
// ("0" for an empty daemon, set by refresh() at status.go:319).
// Without the initial refresh those labels stay at their
// constructor zero-value (empty string), so a regression that
// dropped the initial-fetch line would fail the assertion below.
func TestStatusPollLoopSync(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("anchor")
	defer w.Close()
	w.SetContent(widget.NewLabel("anchor"))

	d := newTestDaemon(t)
	st := buildStatusTab(d)

	// Pre-condition: labels start at the constructor placeholder
	// "-" set by makeLabelGroup. Refresh replaces it with a
	// formatted count. If the placeholder changes, the post-
	// refresh assertion below becomes meaningless — flag it.
	const placeholder = "-"
	if got := st.torrentsLabels[0].Text; got != placeholder {
		t.Fatalf("pre-condition: torrentsLabels[0]=%q, want %q before pollLoop", got, placeholder)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st.pollLoop(ctx)

	// On an empty daemon refresh sets total=0 → "0". Anything
	// that's not the placeholder confirms the initial fetch ran.
	if got := st.torrentsLabels[0].Text; got == placeholder {
		t.Fatalf("torrentsLabels[0] still %q after pollLoop — initial refresh did not run", got)
	}
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
//
// Asserts that the call returns within a deterministic budget
// (pollLoop must honour ctx.Done() before reaching its 4s tick),
// and that the goroutine doesn't hang. A regression that
// dropped the ctx.Done() arm would block the test until the
// 4s tick fires — well past the 200ms budget below.
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

	done := make(chan struct{})
	go func() {
		ct.pollLoop(ctx)
		close(done)
	}()
	select {
	case <-done:
		// Returned promptly — ctx.Done() arm is honoured.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pollLoop did not return within 500ms of canceled ctx — ctx.Done() arm broken")
	}
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
