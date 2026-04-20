package reputation_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestLoadOrCreateTrackerMkdirFails covers the MkdirAll error
// branch — plant a regular file at the path's would-be parent
// so MkdirAll fails with ENOTDIR. Skipped on Windows because
// path-into-file semantics differ.
func TestLoadOrCreateTrackerMkdirFails(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("path-into-file semantics differ on Windows")
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "tracker.json")
	_, err := reputation.LoadOrCreateTracker(bad)
	if err == nil {
		t.Error("LoadOrCreateTracker should fail when parent dir cannot be created")
	}
	if !strings.Contains(err.Error(), "mkdir tracker dir") {
		t.Errorf("err = %q, want it to wrap 'mkdir tracker dir'", err.Error())
	}
}

// TestLoadOrCreateTrackerRecordsNilSubstitute covers the
// `t.Records == nil → make(map)` defensive branch — an existing
// tracker JSON with no "records" key decodes Records as nil; the
// loader must materialise an empty map so subsequent mutations
// don't nil-panic.
func TestLoadOrCreateTrackerRecordsNilSubstitute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "tracker.json")
	// Explicit "records":null forces json.Unmarshal to overwrite
	// the pre-allocated map with nil, exercising the substitute.
	if err := os.WriteFile(path, []byte(`{"records":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := reputation.LoadOrCreateTracker(path)
	if err != nil {
		t.Fatalf("LoadOrCreateTracker: %v", err)
	}
	if tr.Records == nil {
		t.Errorf("Records should be non-nil after substitute")
	}
}
