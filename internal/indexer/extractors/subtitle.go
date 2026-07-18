package extractors

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

// SubtitleExtractor parses SRT and WebVTT subtitle files and returns just
// the dialog text, one subtitle cue per line. Timestamps, cue numbers,
// styling spans, positioning hints, and the WebVTT header are all
// stripped — an FTS index over raw subtitle files wastes space on the
// timecodes and loses phrase coherence at cue boundaries.
//
// The parser is format-tolerant by design: it does not try to validate
// cue numbers or enforce strict WebVTT grammar. Anything that looks like
// "HH:MM:SS,mmm --> HH:MM:SS,mmm" or "HH:MM:SS.mmm --> HH:MM:SS.mmm" is
// treated as a timestamp line; anything below that (until the next blank
// line) is dialog.
//
// Output is exactly ONE Chunk (the full trimmed dialog, Offset 0) —
// subtitle does not run chunkText.
type SubtitleExtractor struct{}

// NewSubtitleExtractor returns a ready-to-use SubtitleExtractor.
func NewSubtitleExtractor() *SubtitleExtractor { return &SubtitleExtractor{} }

// Name implements Extractor.
func (*SubtitleExtractor) Name() string { return "subtitle" }

// srtTimecode matches the start→end line that separates cue metadata
// from cue text in SRT and WebVTT. Accepts both "," and "." as the
// millisecond separator so a single regex handles both formats.
var srtTimecode = regexp.MustCompile(`^\d{1,2}:\d{2}:\d{2}[,.]\d{3}\s*-->\s*\d{1,2}:\d{2}:\d{2}[,.]\d{3}`)

// htmlTag matches simple inline markup that sometimes appears inside
// SRT cues: <i>, <b>, <font color="...">, </i>, etc. Stripped so the
// FTS index doesn't have to deal with noise tokens like "i" or "font".
var htmlTag = regexp.MustCompile(`<[^>]+>`)

// assTag matches ASS-style override blocks like {\an8} or {\pos(100,200)}
// that sometimes leak into SRT exports.
var assTag = regexp.MustCompile(`\{[^}]*\}`)

// subtitleMaxInputBytes is the default read cap for subtitle files
// when the caller passes maxBytes <= 0. A legitimate subtitle file
// never approaches this; the cap exists purely to fail closed on a
// hostile multi-GB ".srt".
const subtitleMaxInputBytes = 16 * 1024 * 1024

// subtitleMaxFileBytes is the dispatch-time size ceiling. No real
// subtitle track exceeds a few MiB of text; anything larger is either
// not a subtitle file or an attempted resource-exhaustion payload.
const subtitleMaxFileBytes = 16 * 1024 * 1024

// Extract implements Extractor. Subtitle files are always small
// (usually <1 MiB) and we want the entire dialog track, but we still
// bound the read via io.LimitReader so a hostile multi-GB ".srt"
// cannot exhaust memory.
func (e *SubtitleExtractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	if maxBytes <= 0 {
		maxBytes = subtitleMaxInputBytes
	}
	var (
		out     strings.Builder
		scanner = bufio.NewScanner(io.LimitReader(r, maxBytes))
	)
	// Subtitle lines can be very long when styling tags are present;
	// give the scanner a generous buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Implicit two-state machine: lines before the first timecode
	// (WebVTT header, NOTE blocks, cue ids) are skipped; a timecode
	// line enters cue state; a blank line leaves it.
	inCue := false

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")

		// Timecode line → start of a new cue. Do not emit the
		// timecode itself; next non-blank line begins dialog.
		if srtTimecode.MatchString(line) {
			inCue = true
			continue
		}

		// WebVTT header ("WEBVTT" plus optional description), NOTE
		// comments, and cue-identifier lines all get skipped.
		if !inCue {
			continue
		}

		// Blank line terminates the current cue.
		if line == "" {
			inCue = false
			out.WriteByte('\n')
			continue
		}

		// Dialog line — strip HTML/ASS overrides.
		clean := htmlTag.ReplaceAllString(line, "")
		clean = assTag.ReplaceAllString(clean, "")
		clean = strings.TrimSpace(clean)
		if clean == "" {
			continue
		}
		out.WriteString(clean)
		out.WriteByte('\n')

		// Output guard: stop accumulating once the dialog text exceeds
		// the byte budget — return the partial text rather than letting
		// a hostile input balloon memory.
		if int64(out.Len()) > maxBytes {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil, nil
	}
	return []Chunk{{Text: text, Offset: 0}}, nil
}

// claimsSubtitle claims the exact subtitle MIMEs only (covering
// .srt/.vtt/.ass/.ssa via extTypes) and refuses anything larger than
// subtitleMaxFileBytes so a hostile multi-GB ".srt" is never opened.
func claimsSubtitle(mime string, c Candidate) bool {
	if c.Size > subtitleMaxFileBytes {
		return false
	}
	switch mime {
	case "application/x-subrip", "text/vtt", "text/x-ssa":
		return true
	}
	return false
}
