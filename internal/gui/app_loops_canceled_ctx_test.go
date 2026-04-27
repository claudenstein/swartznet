package gui

import (
	"context"
	"testing"
)

// TestTitleLoopReturnsOnContextCancel covers titleLoop's
// `case <-ctx.Done(): return` arm at app.go:337-338. With an
// already-canceled context the select picks ctx.Done before
// any tick fires; a.daemon.Eng.* is never dereferenced, so a
// daemon-less App with just a version field is sufficient.
func TestTitleLoopReturnsOnContextCancel(t *testing.T) {
	t.Parallel()
	a := &App{version: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.titleLoop(ctx)
}

// TestNotificationLoopReturnsOnContextCancel covers
// notificationLoop's `case <-ctx.Done(): return` arm at
// app.go:372-373. Same canceled-ctx pattern; the daemon stays
// untouched.
func TestNotificationLoopReturnsOnContextCancel(t *testing.T) {
	t.Parallel()
	a := &App{lastNotified: make(map[string]bool)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.notificationLoop(ctx)
}
