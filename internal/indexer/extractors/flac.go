package extractors

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// FLACExtractor reads Vorbis comments from a FLAC audio file.
// FLAC structure: the 4-byte "fLaC" signature, followed by
// metadata blocks, followed by audio frames. Each metadata
// block is prefixed by a 32-bit header:
//
//	bit 0     = last-metadata-block flag
//	bits 1-7  = block type (0=STREAMINFO, 4=VORBIS_COMMENT, ...)
//	bits 8-31 = body length (big-endian, 24-bit)
//
// We walk the metadata stream, stop at either the VORBIS_COMMENT
// block we care about or end-of-metadata, and extract the
// comment fields (title/artist/album/date/genre/...).
//
// Vorbis comment layout (little-endian):
//
//	vendor_length (u32) | vendor (bytes)
//	comment_count (u32)
//	for each comment: length(u32) | "NAME=value"
type FLACExtractor struct{}

// NewFLACExtractor returns a ready-to-use FLAC extractor.
func NewFLACExtractor() *FLACExtractor { return &FLACExtractor{} }

// Name implements Extractor.
func (*FLACExtractor) Name() string { return "flac" }

// Extract implements Extractor.
func (e *FLACExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024 * 1024
	}
	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("flac: panic: %v", rec)
		}
	}()

	br := bufio.NewReader(io.LimitReader(r, maxBytes))

	// 4-byte "fLaC" signature.
	sig, err := readFull(br, 4)
	if err != nil {
		return nil, fmt.Errorf("flac: read signature: %w", err)
	}
	if string(sig) != "fLaC" {
		return nil, fmt.Errorf("flac: missing fLaC signature")
	}

	for {
		header, err := readFull(br, 4)
		if err != nil {
			return nil, fmt.Errorf("flac: read block header: %w", err)
		}
		isLast := header[0]&0x80 != 0
		blockType := header[0] & 0x7f
		bodyLen := int(header[1])<<16 | int(header[2])<<8 | int(header[3])

		if blockType == 4 {
			body, err := readFull(br, bodyLen)
			if err != nil {
				return nil, fmt.Errorf("flac: read vorbis body: %w", err)
			}
			text := parseVorbisComment(body)
			if text == "" {
				return nil, nil
			}
			return []Chunk{{Text: text}}, nil
		}

		// Skip non-Vorbis block body.
		if _, err := br.Discard(bodyLen); err != nil {
			return nil, fmt.Errorf("flac: discard block: %w", err)
		}
		if isLast {
			break
		}
	}
	return nil, nil
}

// parseVorbisComment returns a "Key: value\n…" rendering of a
// Vorbis comment block. Empty string if the block contains no
// useful fields.
func parseVorbisComment(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	vendorLen := int(binary.LittleEndian.Uint32(b[:4]))
	p := 4 + vendorLen
	if p+4 > len(b) {
		return ""
	}
	count := int(binary.LittleEndian.Uint32(b[p : p+4]))
	p += 4

	var out strings.Builder
	for i := 0; i < count && p+4 <= len(b); i++ {
		ln := int(binary.LittleEndian.Uint32(b[p : p+4]))
		p += 4
		if ln < 0 || p+ln > len(b) {
			break
		}
		comment := string(b[p : p+ln])
		p += ln

		eq := strings.IndexByte(comment, '=')
		if eq < 0 {
			continue
		}
		name := strings.ToUpper(strings.TrimSpace(comment[:eq]))
		value := strings.TrimSpace(comment[eq+1:])
		if value == "" {
			continue
		}
		label := vorbisLabel(name)
		if label == "" {
			continue
		}
		out.WriteString(label)
		out.WriteString(": ")
		out.WriteString(value)
		out.WriteByte('\n')
	}
	return strings.TrimSpace(out.String())
}

// vorbisLabel maps a Vorbis comment field name to a
// user-friendly label, or empty for fields we don't surface.
// Official Vorbis field names: TITLE, VERSION, ALBUM,
// TRACKNUMBER, ARTIST, PERFORMER, COPYRIGHT, LICENSE,
// ORGANIZATION, DESCRIPTION, GENRE, DATE, LOCATION, CONTACT,
// ISRC.
func vorbisLabel(name string) string {
	switch name {
	case "TITLE":
		return "Title"
	case "ARTIST":
		return "Artist"
	case "PERFORMER":
		return "Performer"
	case "ALBUM":
		return "Album"
	case "ALBUMARTIST":
		return "Album artist"
	case "DATE", "YEAR":
		return "Date"
	case "GENRE":
		return "Genre"
	case "TRACKNUMBER":
		return "Track"
	case "DISCNUMBER":
		return "Disc"
	case "COMPOSER":
		return "Composer"
	case "DESCRIPTION", "COMMENT":
		return "Comment"
	case "ORGANIZATION", "LABEL":
		return "Label"
	case "ISRC":
		return "ISRC"
	case "COPYRIGHT":
		return "Copyright"
	}
	return ""
}

func readFull(br *bufio.Reader, n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := io.ReadFull(br, out); err != nil {
		return nil, err
	}
	return out, nil
}

func init() {
	Register(NewFLACExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 1024*1024*1024 {
			return false
		}
		if mime == "audio/flac" || mime == "audio/x-flac" {
			return true
		}
		return strings.HasSuffix(strings.ToLower(c.Path), ".flac")
	})
}
