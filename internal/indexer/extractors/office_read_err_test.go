package extractors

import (
	"errors"
	"testing"
	"testing/iotest"
)

// TestODTExtractReadAllError covers ODTExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm — a faulty Reader
// fails before zip parsing.
func TestODTExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewODTExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}

// TestDOCXExtractReadAllError covers DOCXExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
func TestDOCXExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewDOCXExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}

// TestEPUBExtractReadAllError covers EPUBExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
func TestEPUBExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewEPUBExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}
