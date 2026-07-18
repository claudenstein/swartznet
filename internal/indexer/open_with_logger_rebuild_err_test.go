package indexer

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/blevesearch/bleve/v2"
)

// TestOpenWithLoggerSchemaRebuildRemoveAllError covers
// OpenWithLogger's `os.RemoveAll(path) err → return` arm when
// the schema-rebuild path can't unlink the existing index dir.
//
// Strategy: build a v0-sentinel index, then chmod the parent
// directory to 0o500 so RemoveAll fails (can't unlink children
// without write permission on the parent).
//
// Skipped on Windows + as root.
func TestOpenWithLoggerSchemaRebuildRemoveAllError(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod 0o500 doesn't deny writes")
	}

	parent := t.TempDir()
	path := filepath.Join(parent, "idx")

	// Build a fresh index with the current sentinel, then poke the
	// sentinel back to "0" so the rebuild path triggers.
	idx1, err := OpenWithLogger(path, nil)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := idx1.Close(); err != nil {
		t.Fatal(err)
	}
	bi, err := bleve.Open(path)
	if err != nil {
		t.Fatalf("direct bleve.Open: %v", err)
	}
	if err := bi.SetInternal(schemaSentinelKey, []byte("0")); err != nil {
		t.Fatalf("set stale sentinel: %v", err)
	}
	if err := bi.Close(); err != nil {
		t.Fatal(err)
	}

	// Strip writes on the parent so os.RemoveAll(path) fails.
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := OpenWithLogger(path, log); err == nil {
		t.Error("OpenWithLogger should fail when RemoveAll cannot unlink the stale dir")
	}
}
