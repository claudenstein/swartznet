package indexer

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer/extractors"
)

// panicExtractor is a test-only Extractor that panics inside Extract.
// Used to verify that pipeline.safeExtract converts the panic into
// an ordinary error so one malformed file can't take down the
// pipeline worker goroutine (and, since goroutine panics kill the
// whole process in Go, the daemon itself).
type panicExtractor struct {
	mode string // "string" or "error" or "nil"
}

func (*panicExtractor) Name() string { return "paniker" }

func (e *panicExtractor) Extract(r io.Reader, maxBytes int64) ([]extractors.Chunk, error) {
	// Drain a little input so the test closes the code path that
	// operates on file contents before panicking.
	_, _ = io.CopyN(io.Discard, r, 16)
	switch e.mode {
	case "error":
		panic(errors.New("simulated error panic"))
	case "nil":
		var p *int
		_ = *p // panic: nil pointer dereference
	default:
		panic("simulated string panic")
	}
	return nil, nil
}

func TestSafeExtractConvertsStringPanicToError(t *testing.T) {
	t.Parallel()
	ex := &panicExtractor{mode: "string"}
	chunks, err := safeExtract(ex, strings.NewReader("payload bytes"), 1024)
	if err == nil {
		t.Fatal("expected error from panicking extractor, got nil")
	}
	if chunks != nil {
		t.Errorf("chunks=%v, want nil", chunks)
	}
	if !strings.Contains(err.Error(), "paniker") {
		t.Errorf("error %q does not mention extractor name", err)
	}
	if !strings.Contains(err.Error(), "simulated string panic") {
		t.Errorf("error %q does not include panic message", err)
	}
}

func TestSafeExtractConvertsErrorPanicToError(t *testing.T) {
	t.Parallel()
	ex := &panicExtractor{mode: "error"}
	_, err := safeExtract(ex, strings.NewReader("payload"), 1024)
	if err == nil {
		t.Fatal("expected error from panic(error)")
	}
	if !strings.Contains(err.Error(), "simulated error panic") {
		t.Errorf("error %q does not surface the inner error", err)
	}
}

func TestSafeExtractConvertsNilDerefPanicToError(t *testing.T) {
	t.Parallel()
	ex := &panicExtractor{mode: "nil"}
	_, err := safeExtract(ex, strings.NewReader("payload"), 1024)
	if err == nil {
		t.Fatal("expected error from nil deref")
	}
}

// happyExtractor is the baseline — a successful extractor whose
// output passes through safeExtract unchanged.
type happyExtractor struct{}

func (*happyExtractor) Name() string { return "happy" }

func (*happyExtractor) Extract(r io.Reader, maxBytes int64) ([]extractors.Chunk, error) {
	b, _ := io.ReadAll(r)
	return []extractors.Chunk{{Text: string(b)}}, nil
}

func TestSafeExtractHappyPathIsUnchanged(t *testing.T) {
	t.Parallel()
	ex := &happyExtractor{}
	chunks, err := safeExtract(ex, strings.NewReader("hello"), 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 || chunks[0].Text != "hello" {
		t.Errorf("chunks=%+v, want single 'hello'", chunks)
	}
}
