package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadRecordsHappyPath covers the successful JSONL read with
// blank lines (exercises the `if len(b) == 0 { continue }` arm)
// and a valid record per line.
func TestReadRecordsHappyPath(t *testing.T) {
	t.Parallel()
	const ih = "0123456789abcdef0123456789abcdef01234567" // 40 hex chars
	body := `
{"ih":"` + ih + `","kw":"alpha","t":1}

{"ih":"` + ih + `","kw":"beta","t":2}
`
	path := filepath.Join(t.TempDir(), "input.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := readRecords(path)
	if err != nil {
		t.Fatalf("readRecords: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Kw != "alpha" || recs[1].Kw != "beta" {
		t.Errorf("unexpected records: %+v", recs)
	}
}

// TestReadRecordsOpenError covers the
// `f, err := os.Open(path); if err != nil { return nil, err }` arm.
func TestReadRecordsOpenError(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope.jsonl")
	if _, err := readRecords(missing); err == nil {
		t.Error("readRecords should fail on a missing file")
	}
}

// TestReadRecordsEmptyKwError covers the
// `if jr.Kw == "" { return nil, ... empty kw ... }` arm.
func TestReadRecordsEmptyKwError(t *testing.T) {
	t.Parallel()
	const ih = "0123456789abcdef0123456789abcdef01234567"
	body := `{"ih":"` + ih + `","kw":"","t":1}` + "\n"
	path := filepath.Join(t.TempDir(), "input.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := readRecords(path)
	if err == nil {
		t.Fatal("readRecords should fail on empty kw")
	}
	if !strings.Contains(err.Error(), "empty kw") {
		t.Errorf("unexpected err: %v", err)
	}
}

// TestReadRecordsScannerError covers the
// `if err := scanner.Err(); err != nil` arm. A line longer than
// the 1 MiB scanner buffer triggers bufio.ErrTooLong, which
// surfaces from scanner.Err() after Scan returns false.
func TestReadRecordsScannerError(t *testing.T) {
	t.Parallel()
	const ih = "0123456789abcdef0123456789abcdef01234567"
	// 2 MiB record — exceeds the 1 MiB scanner buffer.
	huge := strings.Repeat("a", 2*1024*1024)
	body := `{"ih":"` + ih + `","kw":"` + huge + `","t":1}` + "\n"
	path := filepath.Join(t.TempDir(), "input.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := readRecords(path); err == nil {
		t.Error("readRecords should fail when a line exceeds the scanner buffer")
	}
}
