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
// Subtitle files for TV shows and movies are often the single most
// valuable text content inside a video torrent — they are the reason
// we special-case this format.
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

// Extract implements Extractor. It ignores its maxBytes parameter
// because subtitle files are always small (usually <1 MiB) and we want
// the entire dialog track.
func (e *SubtitleExtractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	var (
		out     strings.Builder
		scanner = bufio.NewScanner(r)
	)
	// Subtitle lines can be very long when styling tags are present;
	// give the scanner a generous buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// A small state machine with three possible states:
	//   headerPending  — we are still looking at WebVTT header / SRT cue
	//                    numbers, before any real content appears.
	//   cueMeta        — we just saw a timecode line and are now reading
	//                    dialog until the next blank line.
	//   cueText        — inside a dialog block.
	//
	// The enum is implicit: a boolean for "in cue" is enough because
	// header lines are simply ignored by default.
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
		// comments, and cue-identifier lines all get skipped. A WebVTT
		// NOTE block spans to the next blank line.
		if !inCue {
			// Skip WEBVTT header, STYLE and NOTE blocks, and numeric
			// cue ids. If it's a blank line we stay in "not in cue".
			continue
		}

		// Blank line terminates the current cue.
		if line == "" {
			inCue = false
			out.WriteByte('\n')
			continue
		}

		// Dialog line — strip HTML/ASS overrides, unescape HTML
		// entities would belong here too but SRT hardly ever uses
		// them.
		clean := htmlTag.ReplaceAllString(line, "")
		clean = assTag.ReplaceAllString(clean, "")
		clean = strings.TrimSpace(clean)
		if clean == "" {
			continue
		}
		out.WriteString(clean)
		out.WriteByte('\n')
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

func init() {
	Register(NewSubtitleExtractor(), func(mime string, c Candidate) bool {
		switch mime {
		case "application/x-subrip", "text/vtt", "text/x-ssa":
			return true
		}
		return false
	})
}
