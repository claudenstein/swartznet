package extractors

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestSubtitleExtractorAllTagsLineSkipped covers the
// `if clean == "" { continue }` arm. A dialog line that is
// entirely HTML/ASS markup (no visible text) gets stripped to
// empty and must be skipped without emitting a stray newline.
func TestSubtitleExtractorAllTagsLineSkipped(t *testing.T) {
	t.Parallel()
	srt := "1\n00:00:01,000 --> 00:00:02,000\n<i></i>\n\n" +
		"2\n00:00:03,000 --> 00:00:04,000\nhello world\n"
	chunks, err := NewSubtitleExtractor().Extract(strings.NewReader(srt), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	got := chunks[0].Text
	if !strings.Contains(got, "hello world") {
		t.Errorf("dialog missing in output: %q", got)
	}
	if strings.Contains(got, "<i>") {
		t.Errorf("HTML tags leaked into output: %q", got)
	}
}

// errReader returns a hard-coded error on every Read after first
// emitting some valid data so the bufio.Scanner buffers a token.
type errReader struct {
	pre  []byte
	read bool
}

func (e *errReader) Read(p []byte) (int, error) {
	if !e.read {
		e.read = true
		n := copy(p, e.pre)
		return n, nil
	}
	return 0, errors.New("subtitle test: forced read failure")
}

// TestSubtitleExtractorScannerErrorPropagates covers
// `if err := scanner.Err(); err != nil { return nil, err }`.
// A reader that errors mid-scan surfaces the failure as an
// Extract error.
func TestSubtitleExtractorScannerErrorPropagates(t *testing.T) {
	t.Parallel()
	// First chunk: valid SRT cue without trailing newline so the
	// scanner needs another Read to find EOL — which errors.
	pre := bytes.Repeat([]byte("a"), 70*1024) // exceeds the 64 KiB initial buffer to force a refill
	r := &errReader{pre: pre}
	if _, err := NewSubtitleExtractor().Extract(r, 0); err == nil {
		t.Error("expected scanner error to propagate")
	}
}

// satisfy io import.
var _ io.Reader = (*errReader)(nil)
