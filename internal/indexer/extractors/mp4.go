package extractors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// MP4Extractor reads iTunes-style metadata from MP4 / M4A / M4B
// containers. Structure is QuickTime atoms: each atom has a
// 4-byte big-endian size + a 4-byte type + body. Atoms nest:
//
//	ftyp       (file type)
//	moov       (movie box)
//	  udta     (user data)
//	    meta   (metadata)
//	      ilst (item list — iTunes tags)
//	        ©nam (title)
//	        ©ART (artist)
//	        ©alb (album)
//	        ©day (year)
//	        ©gen (genre)
//	        ...
//
// Each iTunes tag atom contains a "data" sub-atom with a type
// flag + reserved + payload. We surface the common text tags.
type MP4Extractor struct{}

// NewMP4Extractor returns a ready-to-use MP4 extractor.
func NewMP4Extractor() *MP4Extractor { return &MP4Extractor{} }

// Name implements Extractor.
func (*MP4Extractor) Name() string { return "mp4" }

// Extract implements Extractor.
func (e *MP4Extractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024
	}
	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("mp4: panic: %v", rec)
		}
	}()

	// MP4 metadata is usually near the start of the file. We
	// read up to 32 MiB to cover videos with trailing moov.
	// For bigger files this is still an upper bound.
	raw, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("mp4: read: %w", err)
	}
	if len(raw) < 8 {
		return nil, fmt.Errorf("mp4: too short")
	}

	// Sanity-check: first atom must be `ftyp`. Avoids
	// false-positive dispatch on arbitrary 8+ byte streams.
	if string(raw[4:8]) != "ftyp" {
		return nil, fmt.Errorf("mp4: missing ftyp atom")
	}

	var out strings.Builder
	walkMP4Atoms(raw, &out, 0)

	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil, nil
	}
	return []Chunk{{Text: text}}, nil
}

// walkMP4Atoms iterates atoms at the current level. When it
// encounters a known container (moov/udta/meta/ilst) it recurses.
// When it hits a known tag atom (©nam etc.) it drills into the
// `data` sub-atom to pull the text payload.
//
// The `meta` atom has a 4-byte version/flags prefix before its
// children — we handle that explicitly.
func walkMP4Atoms(b []byte, out *strings.Builder, depth int) {
	if depth > 8 {
		return
	}
	p := 0
	for p+8 <= len(b) {
		size := int(binary.BigEndian.Uint32(b[p : p+4]))
		atomType := string(b[p+4 : p+8])

		if size == 0 {
			// Atom extends to end of buffer.
			size = len(b) - p
		} else if size == 1 {
			// 64-bit size — uncommon for metadata, skip.
			if p+16 > len(b) {
				return
			}
			size = int(binary.BigEndian.Uint64(b[p+8 : p+16]))
		}
		if size < 8 || p+size > len(b) {
			return
		}
		headerSize := 8
		body := b[p+headerSize : p+size]

		switch atomType {
		case "moov", "udta", "ilst":
			walkMP4Atoms(body, out, depth+1)
		case "meta":
			// 4-byte version/flags before children.
			if len(body) > 4 {
				walkMP4Atoms(body[4:], out, depth+1)
			}
		default:
			// Likely a tag atom if the type starts with ©
			// (0xA9) or is one of the handful of uppercase
			// iTunes tags.
			if label := mp4TagLabel(atomType); label != "" {
				if text := extractDataAtom(body); text != "" {
					out.WriteString(label)
					out.WriteString(": ")
					out.WriteString(text)
					out.WriteByte('\n')
				}
			}
		}
		p += size
	}
}

// mp4TagLabel maps an iTunes-style atom type to a human label,
// or empty for atoms we don't surface.
func mp4TagLabel(atomType string) string {
	switch atomType {
	case "\xA9nam":
		return "Title"
	case "\xA9ART":
		return "Artist"
	case "aART":
		return "Album artist"
	case "\xA9alb":
		return "Album"
	case "\xA9day":
		return "Date"
	case "\xA9gen":
		return "Genre"
	case "gnre":
		return "Genre"
	case "\xA9wrt":
		return "Composer"
	case "\xA9too":
		return "Encoder"
	case "\xA9cmt":
		return "Comment"
	case "desc":
		return "Description"
	case "\xA9grp":
		return "Grouping"
	case "cprt":
		return "Copyright"
	case "trkn":
		return "Track"
	case "disk":
		return "Disc"
	}
	return ""
}

// extractDataAtom finds the nested `data` atom inside an iTunes
// tag atom and returns its text payload.
//
// data atom layout: 8-byte header + 1-byte version + 3-byte
// flags (type) + 4-byte reserved + payload.
func extractDataAtom(b []byte) string {
	p := 0
	for p+16 <= len(b) {
		size := int(binary.BigEndian.Uint32(b[p : p+4]))
		atomType := string(b[p+4 : p+8])
		if size < 16 || p+size > len(b) {
			return ""
		}
		if atomType == "data" {
			// p+8: version(1) + flags(3). flags 1 = UTF-8 text.
			// p+12: reserved.
			payload := b[p+16 : p+size]
			return strings.TrimSpace(string(bytes.TrimRight(payload, "\x00")))
		}
		p += size
	}
	return ""
}

func init() {
	Register(NewMP4Extractor(), func(mime string, c Candidate) bool {
		if c.Size > 64*1024*1024*1024 {
			return false
		}
		switch mime {
		case "video/mp4", "audio/mp4", "audio/x-m4a":
			return true
		}
		lower := strings.ToLower(c.Path)
		return strings.HasSuffix(lower, ".mp4") ||
			strings.HasSuffix(lower, ".m4a") ||
			strings.HasSuffix(lower, ".m4b") ||
			strings.HasSuffix(lower, ".m4v")
	})
}
