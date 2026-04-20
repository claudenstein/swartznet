package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/daemon"
)

// TestLoadFollowFileOpenErrorOtherThanNotExist covers the
// "other os.Open error" branch of LoadFollowFile. We plant a
// regular file then ask for a path "inside" it, so os.Open
// returns ENOTDIR — not os.ErrNotExist — and the warning branch
// fires (rather than the silent-zero ErrNotExist branch).
//
// Skipped on Windows because path-into-file semantics differ.
func TestLoadFollowFileOpenErrorOtherThanNotExist(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("path-into-file semantics differ on Windows")
	}
	dir := t.TempDir()
	parent := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(parent, []byte("hi"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	weird := filepath.Join(parent, "follows.json")

	w := newTestSubscriberWorker(t)
	var stderr bytes.Buffer
	n := daemon.LoadFollowFile(w, weird, &stderr)
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if !strings.Contains(stderr.String(), "follow file") {
		t.Errorf("stderr should mention follow file, got %q", stderr.String())
	}
}
