package trust_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/trust"
)

// TestSaveWriteTempFails covers the os.WriteFile-error branch
// of save — plant a non-empty directory at `<path>.tmp` so the
// truncate-open in os.WriteFile fails. The existing rename
// failure test covers the rename branch; this fills in the
// WriteFile branch.
//
// Skipped on Windows because of differing semantics around
// opening a directory for writing.
func TestSaveWriteTempFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("opening a directory for writing has different semantics on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")

	s, err := trust.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	// Plant a NON-empty directory at the tempfile path so
	// os.WriteFile fails when it tries to truncate-open it.
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	good := strings.Repeat("ab", 32)
	if err := s.Add(good, "first"); err == nil {
		t.Error("Add should fail when save's tempfile path is a non-empty directory")
	}
	// Planted dir must still exist (we didn't accidentally rmdir it).
	if st, err := os.Stat(tmp); err != nil || !st.IsDir() {
		t.Errorf("planted tmp dir disappeared: stat=%v err=%v", st, err)
	}
}
