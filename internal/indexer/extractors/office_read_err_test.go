package extractors

import (
	"errors"
	"testing"
	"testing/iotest"
)

// TestODPExtractReadAllError covers ODPExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm — a faulty Reader
// fails before zip parsing.
func TestODPExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewODPExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}

// TestODTExtractReadAllError covers ODTExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
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

// TestPPTXExtractReadAllError covers PPTXExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
func TestPPTXExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewPPTXExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}

// TestMP4ExtractReadAllError covers MP4Extractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
func TestMP4ExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewMP4Extractor().Extract(r, 0)
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

// TestEXIFExtractReadAllError covers EXIFExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
func TestEXIFExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewEXIFExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}

// TestMOBIExtractReadAllError covers MOBIExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
func TestMOBIExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewMOBIExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}

// TestArchiveExtractReadAllError covers ArchiveExtractor.Extract's
// `io.ReadAll(io.LimitReader(...)) err` arm.
func TestArchiveExtractReadAllError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("simulated read failure"))
	chunks, err := NewArchiveExtractor().Extract(r, 0)
	if err == nil {
		t.Errorf("expected err from faulty reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}
