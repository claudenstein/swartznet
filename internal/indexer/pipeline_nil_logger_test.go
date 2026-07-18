package indexer_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestNewPipelineNilLoggerFallsBackToDefault covers the
// `log == nil → slog.Default()` branch of NewPipeline. Every
// other call site passes a non-nil logger, so this branch was
// never exercised.
func TestNewPipelineNilLoggerFallsBackToDefault(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "nil-logger.bleve"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()
	p := indexer.NewPipeline(idx, nil, 0)
	if p == nil {
		t.Fatal("NewPipeline(nil logger) returned nil")
	}
	// Stop is safe even if Start was never called — drives the
	// idempotent close path without scheduling a worker.
	p.Stop()
}
