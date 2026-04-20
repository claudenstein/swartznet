package indexer

import (
	"path/filepath"
	"testing"

	"github.com/blevesearch/bleve/v2"
)

// TestReadSchemaVersionAtoiError covers the strconv.Atoi error
// branch in readSchemaVersion: a sentinel byte sequence that is
// neither empty nor parseable as an integer must yield 0 (the
// "rebuild" trigger value) rather than panicking.
func TestReadSchemaVersionAtoiError(t *testing.T) {
	t.Parallel()
	// Build a fresh bleve index so we can write the sentinel
	// directly.
	path := filepath.Join(t.TempDir(), "idx")
	bi, err := bleve.New(path, buildMapping())
	if err != nil {
		t.Fatalf("bleve.New: %v", err)
	}
	defer bi.Close()

	if err := bi.SetInternal(schemaSentinelKey, []byte("not-a-number")); err != nil {
		t.Fatalf("SetInternal: %v", err)
	}

	if got := readSchemaVersion(bi); got != 0 {
		t.Errorf("readSchemaVersion(non-integer sentinel) = %d, want 0 (rebuild trigger)", got)
	}
}

// TestReadSchemaVersionEmptySentinel covers the len(val)==0
// branch — the sentinel is present but blank, e.g. from an
// interrupted write.
func TestReadSchemaVersionEmptySentinel(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "idx")
	bi, err := bleve.New(path, buildMapping())
	if err != nil {
		t.Fatalf("bleve.New: %v", err)
	}
	defer bi.Close()

	if err := bi.SetInternal(schemaSentinelKey, []byte{}); err != nil {
		t.Fatalf("SetInternal: %v", err)
	}

	if got := readSchemaVersion(bi); got != 0 {
		t.Errorf("readSchemaVersion(empty sentinel) = %d, want 0", got)
	}
}
