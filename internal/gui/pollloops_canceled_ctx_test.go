package gui

import (
	"context"
	"testing"
)

// TestDownloadsPollLoopReturnsOnContextCancel covers
// downloadsTab.pollLoop's `case <-ctx.Done(): return` arm at
// downloads.go:387-388. With an already-canceled context the
// select picks the canceled-ctx case on the first iteration —
// dl.d is never dereferenced, so a daemon-less downloadsTab is
// fine.
func TestDownloadsPollLoopReturnsOnContextCancel(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl.pollLoop(ctx)
}

// TestFilesDialogPollLoopReturnsOnContextCancel covers
// filesDialog.pollLoop's `case <-ctx.Done(): return` arm at
// files_dialog.go:164-165. Same canceled-ctx pattern; daemon
// stays untouched.
func TestFilesDialogPollLoopReturnsOnContextCancel(t *testing.T) {
	t.Parallel()
	fd := &filesDialog{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fd.pollLoop(ctx)
}
