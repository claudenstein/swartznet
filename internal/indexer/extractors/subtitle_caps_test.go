package extractors

import (
	"io"
	"strings"
	"testing"
)

// TestSubtitleClaimsRejectsHugeFile is the regression for the size
// guard on the subtitle claims func: a "subtitle" file larger than
// the ceiling must not be claimed at all, so a hostile multi-GB .srt
// is never opened.
func TestSubtitleClaimsRejectsHugeFile(t *testing.T) {
	t.Parallel()
	ex, mime := Dispatch(Candidate{
		Path: "evil.srt",
		Size: subtitleMaxFileBytes + 1,
	})
	if ex != nil {
		t.Fatalf("oversized .srt was claimed by %q (mime=%q) — size guard missing", ex.Name(), mime)
	}

	// A normally-sized subtitle file is still claimed.
	ex, _ = Dispatch(Candidate{Path: "ok.srt", Size: 100 * 1024})
	if ex == nil || ex.Name() != "subtitle" {
		t.Fatalf("normal .srt should be claimed by subtitle extractor, got %v", ex)
	}
}

// hugeLineReader yields one enormous never-terminating dialog line. It
// stands in for a hostile subtitle whose body would balloon the output
// builder without the maxBytes/output guard.
type hugeLineReader struct{ remaining int64 }

func (h *hugeLineReader) Read(p []byte) (int, error) {
	if h.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > h.remaining {
		n = int(h.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x' // never a newline => one giant cue line
	}
	h.remaining -= int64(n)
	return n, nil
}

// TestSubtitleExtractRespectsMaxBytes proves the io.LimitReader guard:
// even a 256 MiB hostile stream produces bounded output when a small
// maxBytes budget is passed, rather than reading the whole thing.
func TestSubtitleExtractRespectsMaxBytes(t *testing.T) {
	t.Parallel()

	// Prefix a valid timecode so the parser enters cue state, then feed
	// a huge unterminated dialog line.
	const prefix = "00:00:01,000 --> 00:00:03,500\n"
	r := io.MultiReader(
		strings.NewReader(prefix),
		&hugeLineReader{remaining: 256 * 1024 * 1024},
	)

	const budget = 64 * 1024
	chunks, err := NewSubtitleExtractor().Extract(r, budget)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var total int
	for _, c := range chunks {
		total += len(c.Text)
	}
	// Bounded: well under the 256 MiB the hostile reader would produce.
	// Allow generous slack over the budget for the bufio scanner's
	// in-flight buffer, but it must be orders of magnitude below 256 MiB.
	if total > 4*1024*1024 {
		t.Fatalf("output %d bytes — maxBytes/LimitReader guard not bounding subtitle extraction", total)
	}
}
