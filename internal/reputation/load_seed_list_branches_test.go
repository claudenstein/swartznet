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

// TestLoadSeedListNormalizesUppercaseHex covers the canonical-key
// normalization in LoadSeedList. A valid but upper-case hex pubkey
// in the seed file must be stored under the lowercase key so that
// IsSeeded(lowercase) — the form every lookup path uses — returns
// true. Before normalization the record was keyed by the raw
// upper-case string and the seed bonus silently never fired.
func TestLoadSeedListNormalizesUppercaseHex(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	dir := t.TempDir()
	path := filepath.Join(dir, "seeds.json")

	// 32-byte key (64 hex chars) written entirely upper-case.
	const lower = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	upper := strings.ToUpper(lower)
	if err := os.WriteFile(path, []byte(`{"version":1,"seeds":[{"pubkey":"`+upper+`","label":"curated"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	imported, errs := tr.LoadSeedList(path)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}
	if !tr.IsSeeded(reputation.PubKeyHex(lower)) {
		t.Errorf("IsSeeded(lowercase) = false, want true (seed stored under non-canonical key)")
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
