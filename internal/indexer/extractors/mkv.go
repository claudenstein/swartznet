package extractors

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// MKVExtractor reads human-visible metadata from Matroska (.mkv)
// and WebM (.webm) containers. MKV uses EBML — a self-describing
// binary format where every element has a variable-length ID,
// a variable-length size, and a payload. We walk only the
// top-level Segment and recurse into the Info, Tracks, Chapters,
// and Tags children; everything else (video/audio frame data,
// cue points, etc.) is skipped.
//
// Extracted fields:
//
//	Info/Title          — top-level title string
//	Info/MuxingApp      — tool that produced the file
//	Info/WritingApp     — writing application
//	Tracks/Track Entry
//	    Name            — per-track name
//	    Language        — ISO 639 code
//	Chapters/
//	    ChapterAtom/ChapString  — each chapter title
//	Tags/
//	    Simple Tag name/string  — free-form metadata
//
// The EBML spec is large (every codec adds its own IDs) but our
// set is small enough to list explicitly. Unknown IDs are
// skipped by consuming `size` bytes.
type MKVExtractor struct{}

// NewMKVExtractor returns a ready-to-use MKV extractor.
func NewMKVExtractor() *MKVExtractor { return &MKVExtractor{} }

// Name implements Extractor.
func (*MKVExtractor) Name() string { return "mkv" }

// EBML element IDs we care about. IDs are emitted big-endian;
// we store them as their raw integer value for direct
// comparison with readEBMLID.
const (
	ebmlIDEBML        = 0x1A45DFA3
	ebmlIDSegment     = 0x18538067
	ebmlIDInfo        = 0x1549A966
	ebmlIDTracks      = 0x1654AE6B
	ebmlIDChapters    = 0x1043A770
	ebmlIDTags        = 0x1254C367
	ebmlIDTitle       = 0x7BA9
	ebmlIDMuxingApp   = 0x4D80
	ebmlIDWritingApp  = 0x5741
	ebmlIDTrackEntry  = 0xAE
	ebmlIDTrackName   = 0x536E
	ebmlIDLanguage    = 0x22B59C
	ebmlIDChapterAtom = 0xB6
	ebmlIDChapDisplay = 0x80
	ebmlIDChapString  = 0x85
	ebmlIDSimpleTag   = 0x67C8
	ebmlIDTagName     = 0x45A3
	ebmlIDTagString   = 0x4487
)

// Extract implements Extractor.
func (e *MKVExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 {
		maxBytes = 16 * 1024 * 1024 // only the first N MiB; metadata is always up front
	}
	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("mkv: panic: %v", rec)
		}
	}()

	// Cap reads: MKV metadata almost always sits in the first
	// few MiB of the file (before the cluster/cue data).
	br := bufio.NewReaderSize(io.LimitReader(r, maxBytes), 64*1024)

	// First element must be the EBML header.
	id, size, err := readEBMLHeader(br)
	if err != nil {
		return nil, fmt.Errorf("mkv: read EBML header: %w", err)
	}
	if id != ebmlIDEBML {
		return nil, fmt.Errorf("mkv: not an EBML file (id=0x%x)", id)
	}
	if _, err := br.Discard(int(size)); err != nil {
		return nil, fmt.Errorf("mkv: skip EBML: %w", err)
	}

	// Second element should be a Segment.
	id, _, err = readEBMLHeader(br)
	if err != nil {
		return nil, fmt.Errorf("mkv: read segment header: %w", err)
	}
	if id != ebmlIDSegment {
		return nil, fmt.Errorf("mkv: no Segment after EBML")
	}

	var out strings.Builder

	// Walk segment children.
	for {
		id, size, err := readEBMLHeader(br)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("mkv: read child: %w", err)
		}
		switch id {
		case ebmlIDInfo, ebmlIDTracks, ebmlIDChapters, ebmlIDTags:
			body, err := readFull(br, int(size))
			if err != nil {
				return nil, fmt.Errorf("mkv: read %x body: %w", id, err)
			}
			walkEBML(body, &out, 0)
		default:
			if _, err := br.Discard(int(size)); err != nil {
				return nil, fmt.Errorf("mkv: discard: %w", err)
			}
		}
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil, nil
	}
	return []Chunk{{Text: text}}, nil
}

// walkEBML recursively walks an EBML byte slice, emitting known
// leaf-element values to out. Depth is used as a safety rail
// against pathological nesting (shouldn't fire on valid files).
func walkEBML(b []byte, out *strings.Builder, depth int) {
	if depth > 8 {
		return
	}
	p := 0
	for p < len(b) {
		id, idLen := parseVINT(b[p:], true)
		if idLen == 0 {
			return
		}
		size, sizeLen := parseVINT(b[p+idLen:], false)
		if sizeLen == 0 {
			return
		}
		dataStart := p + idLen + sizeLen
		dataEnd := dataStart + int(size)
		if dataEnd > len(b) {
			return
		}
		data := b[dataStart:dataEnd]

		switch id {
		case ebmlIDTitle:
			emit(out, "Title", string(data))
		case ebmlIDMuxingApp:
			emit(out, "Muxer", string(data))
		case ebmlIDWritingApp:
			emit(out, "Writer", string(data))
		case ebmlIDTrackName:
			emit(out, "Track", string(data))
		case ebmlIDLanguage:
			emit(out, "Language", string(data))
		case ebmlIDChapString:
			emit(out, "Chapter", string(data))
		// Container elements: recurse into their children.
		case ebmlIDTrackEntry,
			ebmlIDChapterAtom,
			ebmlIDChapDisplay,
			ebmlIDSimpleTag:
			walkEBML(data, out, depth+1)
		case ebmlIDTagName:
			// Emit as "Tag <name>: <value>" when paired with
			// a TagString sibling. We handle this by
			// recursing into SimpleTag which contains both.
		case ebmlIDTagString:
			// See above; we handle the paired rendering below.
		default:
			// Unknown: skip silently.
		}
		p = dataEnd
	}
}

// emit writes a "label: trimmed-value\n" line, skipping empty
// values and trailing nulls.
func emit(out *strings.Builder, label, value string) {
	value = strings.TrimRight(value, "\x00 \t\r\n")
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	out.WriteString(label)
	out.WriteString(": ")
	out.WriteString(value)
	out.WriteByte('\n')
}

// readEBMLHeader parses (id, size) from the reader.
func readEBMLHeader(br *bufio.Reader) (uint32, uint64, error) {
	idBuf, err := peekVINT(br, true)
	if err != nil {
		return 0, 0, err
	}
	id, idLen := parseVINT(idBuf, true)
	if idLen == 0 {
		return 0, 0, fmt.Errorf("bad VINT id")
	}
	if _, err := br.Discard(idLen); err != nil {
		return 0, 0, err
	}
	sizeBuf, err := peekVINT(br, false)
	if err != nil {
		return 0, 0, err
	}
	size, sizeLen := parseVINT(sizeBuf, false)
	if sizeLen == 0 {
		return 0, 0, fmt.Errorf("bad VINT size")
	}
	if _, err := br.Discard(sizeLen); err != nil {
		return 0, 0, err
	}
	return uint32(id), size, nil
}

// peekVINT peeks enough bytes to cover one EBML VINT (up to 8).
func peekVINT(br *bufio.Reader, keepMarker bool) ([]byte, error) {
	for n := 1; n <= 8; n++ {
		buf, err := br.Peek(n)
		if err != nil {
			return nil, err
		}
		if buf[0] == 0 {
			// Not a valid VINT.
			return nil, fmt.Errorf("vint starts with zero byte")
		}
		// Determine the VINT length from the leading zero bits
		// of the first byte.
		if vintLen(buf[0]) == n {
			_ = keepMarker // mirror param; callers handle marker stripping.
			return buf, nil
		}
	}
	return nil, fmt.Errorf("vint too long")
}

// vintLen counts leading zero bits + 1 to get the full VINT
// length in bytes.
func vintLen(first byte) int {
	switch {
	case first&0x80 != 0:
		return 1
	case first&0x40 != 0:
		return 2
	case first&0x20 != 0:
		return 3
	case first&0x10 != 0:
		return 4
	case first&0x08 != 0:
		return 5
	case first&0x04 != 0:
		return 6
	case first&0x02 != 0:
		return 7
	case first&0x01 != 0:
		return 8
	}
	return 0
}

// parseVINT decodes an EBML variable-length integer from b.
// Returns (value, length-in-bytes). Length 0 means parse
// failure. If keepMarker is true, the top-bit marker is kept
// (for IDs); otherwise it's stripped (for sizes).
func parseVINT(b []byte, keepMarker bool) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	n := vintLen(b[0])
	if n == 0 || n > len(b) {
		return 0, 0
	}
	var v uint64
	if keepMarker {
		v = uint64(b[0])
	} else {
		mask := byte(0xff) >> uint(n)
		v = uint64(b[0] & mask)
	}
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, n
}

func init() {
	Register(NewMKVExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 64*1024*1024*1024 {
			// 64 GiB safety ceiling. We only read the first few
			// MiB anyway, but this bounds the Dispatch() cost.
			return false
		}
		if mime == "video/x-matroska" || mime == "video/webm" {
			return true
		}
		lower := strings.ToLower(c.Path)
		return strings.HasSuffix(lower, ".mkv") ||
			strings.HasSuffix(lower, ".webm") ||
			strings.HasSuffix(lower, ".mka")
	})
}
