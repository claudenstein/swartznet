package signing_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/signing"
)

// TestSignFileRenameDirReadOnly covers SignFile's rename-error
// arm at lines 136-139. Read+WriteFile both succeed, then
// rename(tmp, path) fails because the parent dir lacks write
// permission. The deferred cleanup os.Remove(tmp) also fails
// silently, which is fine — we only care that "signing: rename"
// surfaces.
//
// Skipped on Windows because EACCES vs ERROR_ACCESS_DENIED and
// the dir-perm semantics diverge.
func TestSignFileRenameDirReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dir write-permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0500 doesn't restrict rename for uid 0")
	}
	t.Parallel()
	_, priv := newKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "input.torrent")
	if err := os.WriteFile(path, miniTorrent(t), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-create the tmp file so SignFile's WriteFile can open it
	// for truncate-write without needing the parent dir's write
	// bit (only the file's own perm matters).
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := signing.SignFile(path, priv)
	if err == nil {
		t.Fatal("SignFile should error when rename can't replace path")
	}
	if got := err.Error(); !strings.Contains(got, "rename") {
		t.Errorf("expected 'rename' in err, got %q", got)
	}
}
