package extractors

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// ID3Extractor reads ID3v2 tag metadata from .mp3 files. Just
// the tags — we don't decode audio data.
//
// ID3v2 tag structure (versions 2.3 and 2.4 covered):
//
//	10-byte header: "ID3" v1 v2 flags sizesyncsafe(4)
//	frames: 10-byte header (ID4 + size(4) + flags(2)) + body
//
// The sizesyncsafe format is little-unique — 4 bytes with the MSB
// of each zeroed, so only 7 bits per byte count. It's big-endian
// across the 4 bytes.
//
// Frames we surface:
//
//	TIT2  Title
//	TPE1  Artist
//	TALB  Album
//	TDRC  Year (v2.4) / TYER (v2.3)
//	TCON  Genre
//	TRCK  Track
//	COMM  Comment
//
// Text frames carry an encoding byte (0=ISO-8859-1, 1=UTF-16
// w/BOM, 2=UTF-16 BE, 3=UTF-8) followed by the text.
type ID3Extractor struct{}

// NewID3Extractor returns a ready-to-use ID3 extractor.
func NewID3Extractor() *ID3Extractor { return &ID3Extractor{} }

// Name implements Extractor.
func (*ID3Extractor) Name() string { return "id3" }

// Extract implements Extractor.
func (e *ID3Extractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}

	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("id3: panic during extraction: %v", rec)
		}
	}()

	br := bufio.NewReader(io.LimitReader(r, maxBytes))
	header, err := br.Peek(10)
	if err != nil || len(header) < 10 {
		return nil, fmt.Errorf("id3: file too short")
	}
	if string(header[:3]) != "ID3" {
		return nil, fmt.Errorf("id3: missing ID3 header")
	}
	majorVer := header[3]
	if majorVer != 3 && majorVer != 4 {
		// v2.2 has a different frame layout; skip for now.
		return nil, fmt.Errorf("id3: unsupported version 2.%d", majorVer)
	}
	tagSize := syncSafeInt(header[6:10])
	totalTagSize := 10 + tagSize
	raw := make([]byte, totalTagSize)
	if _, err := io.ReadFull(br, raw); err != nil {
		return nil, fmt.Errorf("id3: read tag: %w", err)
	}
	frames := raw[10:]

	var out strings.Builder
	for len(frames) >= 10 {
		id := string(frames[:4])
		// First null ID signals end of frames / padding area.
		if id[0] == 0 {
			break
		}
		var frameSize int
		if majorVer == 4 {
			frameSize = syncSafeInt(frames[4:8])
		} else {
			frameSize = int(binary.BigEndian.Uint32(frames[4:8]))
		}
		if frameSize < 0 || 10+frameSize > len(frames) {
			break
		}
		body := frames[10 : 10+frameSize]
		frames = frames[10+frameSize:]

		label := id3Label(id)
		if label == "" {
			continue
		}
		text := decodeID3Text(body, id)
		if text == "" {
			continue
		}
		out.WriteString(label)
		out.WriteString(": ")
		out.WriteString(text)
		out.WriteByte('\n')
	}

	final := strings.TrimSpace(out.String())
	if final == "" {
		return nil, nil
	}
	return []Chunk{{Text: final}}, nil
}

// syncSafeInt decodes 4 syncsafe bytes (MSB of each ignored) into
// a single 28-bit integer, big-endian across the bytes.
func syncSafeInt(b []byte) int {
	return int(b[0]&0x7f)<<21 |
		int(b[1]&0x7f)<<14 |
		int(b[2]&0x7f)<<7 |
		int(b[3]&0x7f)
}

// id3Label maps a frame ID to a user-friendly label, or empty
// for frames we don't surface.
func id3Label(id string) string {
	switch id {
	case "TIT2":
		return "Title"
	case "TPE1":
		return "Artist"
	case "TPE2":
		return "Album artist"
	case "TALB":
		return "Album"
	case "TDRC", "TYER":
		return "Year"
	case "TCON":
		return "Genre"
	case "TRCK":
		return "Track"
	case "TPUB":
		return "Publisher"
	case "COMM":
		return "Comment"
	case "USLT":
		return "Lyrics"
	}
	return ""
}

// decodeID3Text unwraps an ID3 text frame body: first byte is
// the encoding, remaining bytes are the text (often with a
// trailing null byte). COMM/USLT frames have extra language +
// descriptor prefixes; we skip those to reach the payload.
func decodeID3Text(body []byte, frameID string) string {
	if len(body) == 0 {
		return ""
	}
	enc := body[0]
	text := body[1:]

	// COMM and USLT have: [enc(1)][lang(3)][desc NUL-term][payload].
	// Skip lang + desc.
	if frameID == "COMM" || frameID == "USLT" {
		if len(text) < 3 {
			return ""
		}
		text = text[3:] // skip language
		// Strip descriptor: for enc 0 / 3 that's a NUL; for
		// enc 1 / 2 that's a double NUL (UTF-16).
		if enc == 0 || enc == 3 {
			if i := bytes.IndexByte(text, 0); i >= 0 {
				text = text[i+1:]
			}
		} else {
			if i := findDoubleNul(text); i >= 0 {
				text = text[i+2:]
			}
		}
	}

	return decodeID3Bytes(enc, text)
}

func decodeID3Bytes(enc byte, b []byte) string {
	// Strip trailing NULs common in all encodings.
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return ""
	}
	switch enc {
	case 0:
		// ISO-8859-1 — widen byte-by-byte to runes.
		out := make([]rune, 0, len(b))
		for _, c := range b {
			out = append(out, rune(c))
		}
		return strings.TrimSpace(string(out))
	case 1:
		// UTF-16 with BOM.
		if len(b) < 2 {
			return ""
		}
		var src []uint16
		switch {
		case b[0] == 0xff && b[1] == 0xfe:
			src = u16LE(b[2:])
		case b[0] == 0xfe && b[1] == 0xff:
			src = u16BE(b[2:])
		default:
			src = u16LE(b) // best guess
		}
		return strings.TrimSpace(string(utf16.Decode(src)))
	case 2:
		return strings.TrimSpace(string(utf16.Decode(u16BE(b))))
	case 3:
		return strings.TrimSpace(string(b))
	}
	return strings.TrimSpace(string(b))
}

func u16LE(b []byte) []uint16 {
	out := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		out = append(out, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return out
}

func u16BE(b []byte) []uint16 {
	out := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		out = append(out, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return out
}

func findDoubleNul(b []byte) int {
	for i := 0; i+1 < len(b); i += 2 {
		if b[i] == 0 && b[i+1] == 0 {
			return i
		}
	}
	return -1
}

func init() {
	Register(NewID3Extractor(), func(mime string, c Candidate) bool {
		if c.Size > 500*1024*1024 {
			return false
		}
		if mime == "audio/mpeg" || mime == "audio/mp3" {
			return true
		}
		return strings.HasSuffix(strings.ToLower(c.Path), ".mp3")
	})
}
