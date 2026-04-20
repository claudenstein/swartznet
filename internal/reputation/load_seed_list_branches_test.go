package reputation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestLoadSeedListEmptyPathNoop covers the `path == "" → 0, nil`
// fast return. The remaining LoadSeedList tests use real files,
// so this fills in the empty-path branch.
func TestLoadSeedListEmptyPathNoop(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	imported, errs := tr.LoadSeedList("")
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
	if errs != nil {
		t.Errorf("errs = %v, want nil", errs)
	}
}

// TestLoadSeedListReadErrorIsReturned covers the
// non-ErrNotExist ReadFile error branch: pointing the path at a
// directory makes os.ReadFile fail with "is a directory", which
// is NOT ErrNotExist so the wrapped "read seed list" error
// must propagate.
func TestLoadSeedListReadErrorIsReturned(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	imported, errs := tr.LoadSeedList(t.TempDir()) // path is a dir
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
	if len(errs) == 0 {
		t.Fatal("expected at least one error")
	}
	if !strings.Contains(errs[0].Error(), "read seed list") {
		t.Errorf("err = %q, want it to wrap 'read seed list'", errs[0].Error())
	}
}

// TestLoadSeedListBadJSON covers the json.Unmarshal-error branch.
func TestLoadSeedListBadJSON(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	dir := t.TempDir()
	path := filepath.Join(dir, "seeds.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	imported, errs := tr.LoadSeedList(path)
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "parse seed list") {
		t.Errorf("errs = %v, want a 'parse seed list' wrapped err", errs)
	}
}

// TestLoadSeedListUnsupportedVersion covers the
// `list.Version != 1 → unsupported version` branch.
func TestLoadSeedListUnsupportedVersion(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	dir := t.TempDir()
	path := filepath.Join(dir, "seeds.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"seeds":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	imported, errs := tr.LoadSeedList(path)
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "unsupported seed list version") {
		t.Errorf("errs = %v, want an 'unsupported seed list version' err", errs)
	}
}
