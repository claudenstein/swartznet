package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOpenOrCreateBleveOpenFails covers the bleve.Open error
// branch of openOrCreate. Plant a directory whose index_meta.json
// is garbage — indexDirExists returns true so we go through the
// Open path, but Open then fails to parse the metadata.
func TestOpenOrCreateBleveOpenFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake.bleve")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a stub index_meta.json so indexDirExists() == true
	// but the body is invalid JSON, which makes bleve.Open error.
	if err := os.WriteFile(filepath.Join(path, "index_meta.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openOrCreate(path); err == nil {
		t.Error("openOrCreate should error when index_meta.json is corrupt")
	}
}

// TestOpenOrCreateBleveNewFails covers the bleve.New error
// branch — point at a path that is itself a regular file, so
// indexDirExists returns false (no marker inside) and bleve.New
// fails because it can't make a directory at a file.
func TestOpenOrCreateBleveNewFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(path, []byte("not a bleve dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openOrCreate(path); err == nil {
		t.Error("openOrCreate should error when path is a regular file")
	}
}
