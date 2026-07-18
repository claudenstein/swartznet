package indexer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestOpenWithLoggerOpenOrCreateError covers OpenWithLogger's
// `bi, err := openOrCreate(path); if err != nil { return nil, err }`
// arm. Pointing the index path at a *file* (not a directory)
// makes Bleve's create call fail because the path already exists
// as a non-index regular file.
func TestOpenWithLoggerOpenOrCreateError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "blocked")
	if err := os.WriteFile(path, []byte("not a bleve index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := indexer.OpenWithLogger(path, nil); err == nil {
		t.Error("OpenWithLogger should fail when path is an existing regular file")
	}
}
