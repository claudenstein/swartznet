package indexer

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

// TestPipelineRunReturnsOnClosedInput covers Pipeline.run's
// `case in, ok := <-p.input: if !ok { return }` arm at
// pipeline.go:143-146. Production code only closes the input
// channel via Stop (which triggers stopCh), but the explicit
// !ok check guards against a future caller that might close
// p.input directly. Force-close it via the internal field so
// the run goroutine exits on the closed-channel read.
func TestPipelineRunReturnsOnClosedInput(t *testing.T) {
	t.Parallel()
	idx, err := Open(filepath.Join(t.TempDir(), "p.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	p := NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	p.Start()

	// Close the input channel directly; run's select will pick
	// the closed-channel case and return. Stop() relies on stopCh,
	// not channel-close, so this exercises the alternative exit.
	close(p.input)

	// Wait for the worker goroutine to finish via wg. If the !ok
	// arm didn't fire, this hangs and the test times out.
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Pipeline.run did not exit on closed input within 2s")
	}
}
