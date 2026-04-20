package indexer_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestCloseIsIdempotent covers the documented "Close is
// idempotent" contract: calling Close twice in a row must not
// error and must hit the `i.bleve == nil` early-return branch
// on the second call.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "idx.bleve")
	idx, err := indexer.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must be a noop, not an error.
	if err := idx.Close(); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
	// Third one for good measure.
	if err := idx.Close(); err != nil {
		t.Errorf("third Close: %v, want nil", err)
	}
}
