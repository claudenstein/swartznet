package indexer

import (
	"bytes"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
)

// TestOpenWithLoggerSchemaRebuild covers the previously-uncovered
// schema-mismatch rebuild branch. Strategy:
//   1. Open a fresh index — writeSchemaVersion stores "3".
//   2. Close, then reopen with bleve directly and overwrite the
//      sentinel to "1" so the next OpenWithLogger sees a stale
//      schema.
//   3. OpenWithLogger again — must detect the mismatch, log the
//      rebuild warning, RemoveAll the directory, and create a
//      fresh index.
func TestOpenWithLoggerSchemaRebuild(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "idx")

	// Step 1: fresh index, sentinel = current SchemaVersion.
	idx1, err := OpenWithLogger(path, nil)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Seed a torrent so we can prove the rebuild wiped the data.
	if err := idx1.IndexTorrent(TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "before-rebuild",
	}); err != nil {
		t.Fatal(err)
	}
	if err := idx1.Close(); err != nil {
		t.Fatal(err)
	}

	// Step 2: poke the sentinel directly to an obsolete version so
	// the next OpenWithLogger sees the mismatch.
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

	// Step 3: OpenWithLogger should detect the stale sentinel,
	// emit the warn-level rebuild message, wipe the dir, and
	// return a fresh empty index.
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	idx2, err := OpenWithLogger(path, log)
	if err != nil {
		t.Fatalf("rebuild open: %v", err)
	}
	defer idx2.Close()

	if !strings.Contains(buf.String(), "schema_rebuild") {
		t.Errorf("expected schema_rebuild warn in log, got %q", buf.String())
	}

	count, err := idx2.DocCount()
	if err != nil {
		t.Fatalf("DocCount: %v", err)
	}
	if count != 0 {
		t.Errorf("DocCount after rebuild = %d, want 0 (rebuild should wipe)", count)
	}
}

// readBuf is a tiny helper so the test asserts on the captured
// log without pulling in extra imports.
var _ = io.Discard
