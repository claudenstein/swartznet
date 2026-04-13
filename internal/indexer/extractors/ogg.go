package extractors

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// OGGExtractor reads Vorbis comments from an Ogg Vorbis audio
// file (.ogg / .oga). Ogg is a container format; each page
// begins with the "OggS" capture pattern followed by a 27-byte
// header + segment table + packet data. Vorbis packets carry
// a type byte: 0x01 identification, 0x03 comment, 0x05 setup.
// We walk pages until we find a packet starting with 0x03 +
// "vorbis", then reuse parseVorbisComment.
//
// This scope covers Ogg Vorbis specifically. Ogg Opus uses a
// slightly different comment format ("OpusTags") with the
// same overall shape; we also handle Opus by sniffing the
// leading signature.
type OGGExtractor struct{}

// NewOGGExtractor returns a ready-to-use OGG extractor.
func NewOGGExtractor() *OGGExtractor { return &OGGExtractor{} }

// Name implements Extractor.
func (*OGGExtractor) Name() string { return "ogg" }

// Extract implements Extractor.
func (e *OGGExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024 * 1024
	}
	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("ogg: panic: %v", rec)
		}
	}()

	br := bufio.NewReader(io.LimitReader(r, 4*1024*1024))

	var packet bytes.Buffer
	for pageCount := 0; pageCount < 32; pageCount++ {
		page, err := readOGGPage(br)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ogg: read page: %w", err)
		}
		packet.Write(page.data)

		// Only process once the packet completes (no
		// continuation-flag logic — we just check the buffer
		// for known packet headers).
		buf := packet.Bytes()
		// Ogg Vorbis: packet type 0x03 + "vorbis"
		if len(buf) >= 7 && buf[0] == 0x03 && string(buf[1:7]) == "vorbis" {
			text := parseVorbisComment(buf[7:])
			if text == "" {
				return nil, nil
			}
			return []Chunk{{Text: text}}, nil
		}
		// Ogg Opus: "OpusTags" signature
		if len(buf) >= 8 && string(buf[:8]) == "OpusTags" {
			text := parseVorbisComment(buf[8:])
			if text == "" {
				return nil, nil
			}
			return []Chunk{{Text: text}}, nil
		}
	}
	return nil, nil
}

type oggPage struct {
	data []byte
}

// readOGGPage reads one Ogg page and returns its packet bytes.
func readOGGPage(br *bufio.Reader) (oggPage, error) {
	header, err := readFull(br, 27)
	if err != nil {
		return oggPage{}, err
	}
	if string(header[:4]) != "OggS" {
		return oggPage{}, fmt.Errorf("ogg: bad capture pattern")
	}
	segCount := int(header[26])
	segTable, err := readFull(br, segCount)
	if err != nil {
		return oggPage{}, err
	}
	var total int
	for _, s := range segTable {
		total += int(s)
	}
	body, err := readFull(br, total)
	if err != nil {
		return oggPage{}, err
	}
	return oggPage{data: body}, nil
}

// Keep binary.LittleEndian import required by the parse path.
var _ = binary.LittleEndian

func init() {
	Register(NewOGGExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 1024*1024*1024 {
			return false
		}
		switch mime {
		case "audio/ogg", "audio/x-ogg", "audio/vorbis", "audio/opus":
			return true
		}
		lower := strings.ToLower(c.Path)
		return strings.HasSuffix(lower, ".ogg") ||
			strings.HasSuffix(lower, ".oga") ||
			strings.HasSuffix(lower, ".opus")
	})
}
