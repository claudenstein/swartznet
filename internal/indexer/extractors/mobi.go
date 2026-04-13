package extractors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// MOBIExtractor reads metadata from Amazon Kindle .mobi / .azw /
// .azw3 ebook files. MOBI is built on top of the PalmDOC
// container; the first record always begins with a PalmDB
// header, followed by the MOBI header, optionally followed by
// an EXTH block with the book's title, author, publisher,
// description, etc.
//
// Scope: we extract metadata only — title, author, publisher,
// description, ISBN. The book text itself lives in subsequent
// records and uses a PalmDOC / HUFF/CDIC compression scheme
// that a minimal extractor won't justify. Metadata alone is
// plenty for search: users typically want to match by book
// title and author anyway.
//
// Format references:
//   - PalmDB: https://wiki.mobileread.com/wiki/PDB
//   - MOBI:   https://wiki.mobileread.com/wiki/MOBI
//   - EXTH:   https://wiki.mobileread.com/wiki/MOBI#EXTH_Header
type MOBIExtractor struct{}

// NewMOBIExtractor returns a ready-to-use MOBI extractor.
func NewMOBIExtractor() *MOBIExtractor { return &MOBIExtractor{} }

// Name implements Extractor.
func (*MOBIExtractor) Name() string { return "mobi" }

// Extract implements Extractor. Returns one chunk per metadata
// field ("Title: ...", "Author: ...", etc.) concatenated into a
// single whole-file chunk.
func (e *MOBIExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 {
		maxBytes = 16 * 1024 * 1024
	}

	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("mobi: panic during extraction: %v", rec)
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("mobi: read: %w", err)
	}

	// PalmDB header is 78 bytes. Record 0 starts at the offset
	// stored at bytes 78+... actually: bytes 76-79 is "num records",
	// then each record has an 8-byte index entry. Simplified:
	//   0-32   — name (32 bytes, null-padded)
	//   76-78  — number of records (uint16 BE)
	//   78+    — record info list, 8 bytes each:
	//              offset (uint32 BE) | attrs+uid (4 bytes)
	if len(buf) < 78+8 {
		return nil, fmt.Errorf("mobi: file too short (%d bytes)", len(buf))
	}
	numRecords := binary.BigEndian.Uint16(buf[76:78])
	if numRecords == 0 {
		return nil, fmt.Errorf("mobi: no records")
	}

	// First record starts at offset stored at buf[78:82].
	rec0Offset := int(binary.BigEndian.Uint32(buf[78:82]))
	if rec0Offset >= len(buf) {
		return nil, fmt.Errorf("mobi: record 0 offset out of range")
	}
	rec0 := buf[rec0Offset:]

	// Record 0 layout:
	//   0-15   — PalmDOC header (compression, etc.)
	//   16+    — MOBI header:
	//             0-3   identifier ("MOBI")
	//             8-11  mobi type
	//             12-15 text encoding (1252 or 65001)
	//             84-87 EXTH flags (if bit 6 set, EXTH follows)
	//             108-119 full name offset/length in record 0
	//             etc.
	if len(rec0) < 16+88 {
		return nil, fmt.Errorf("mobi: record 0 too short")
	}
	mobiHeader := rec0[16:]
	if string(mobiHeader[:4]) != "MOBI" {
		return nil, fmt.Errorf("mobi: missing MOBI signature")
	}
	mobiHeaderLen := binary.BigEndian.Uint32(mobiHeader[4:8])
	if mobiHeaderLen < 16 || int(mobiHeaderLen) > len(mobiHeader) {
		return nil, fmt.Errorf("mobi: bad header length %d", mobiHeaderLen)
	}
	textEncoding := binary.BigEndian.Uint32(mobiHeader[12:16])

	var out strings.Builder

	// Extract the "full name" (book title) — stored at an offset
	// given by mobiHeader[84:88], with length at mobiHeader[88:92].
	if mobiHeaderLen >= 92 {
		titleOffset := int(binary.BigEndian.Uint32(mobiHeader[84:88]))
		titleLen := int(binary.BigEndian.Uint32(mobiHeader[88:92]))
		// The offset is relative to the start of record 0.
		if titleOffset >= 0 && titleLen > 0 && titleOffset+titleLen <= len(rec0) {
			title := decodeMOBIText(rec0[titleOffset:titleOffset+titleLen], textEncoding)
			if title != "" {
				out.WriteString("Title: ")
				out.WriteString(title)
				out.WriteByte('\n')
			}
		}
	}

	// EXTH block. Its flags are at mobiHeader[128:132] in the
	// extended MOBI header (>= v2). Bit 6 (0x40) means "EXTH
	// follows the MOBI header".
	if mobiHeaderLen >= 132 {
		exthFlags := binary.BigEndian.Uint32(mobiHeader[128:132])
		if exthFlags&0x40 != 0 {
			exthStart := int(mobiHeaderLen) + 16 // 16 = PalmDOC header length
			if exthStart < len(rec0) {
				if extracted := extractEXTH(rec0[exthStart:], textEncoding); extracted != "" {
					out.WriteString(extracted)
				}
			}
		}
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil, nil
	}
	return []Chunk{{Text: text}}, nil
}

// extractEXTH walks the EXTH record list starting at b.
//
// EXTH layout:
//
//	0-3   identifier ("EXTH")
//	4-7   header length (uint32 BE)
//	8-11  record count (uint32 BE)
//	12+   records: [type uint32][length uint32][data length-8 bytes]
//
// Known record types we surface:
//
//	100   author
//	101   publisher
//	103   description
//	104   ISBN
//	105   subject
//	106   published date
//	503   updated title
//	524   language
func extractEXTH(b []byte, encoding uint32) string {
	if len(b) < 12 || string(b[:4]) != "EXTH" {
		return ""
	}
	count := binary.BigEndian.Uint32(b[8:12])
	p := 12

	var out strings.Builder
	for i := uint32(0); i < count && p+8 <= len(b); i++ {
		recType := binary.BigEndian.Uint32(b[p : p+4])
		recLen := int(binary.BigEndian.Uint32(b[p+4 : p+8]))
		if recLen < 8 || p+recLen > len(b) {
			break
		}
		data := b[p+8 : p+recLen]
		p += recLen

		var label string
		switch recType {
		case 100:
			label = "Author"
		case 101:
			label = "Publisher"
		case 103:
			label = "Description"
		case 104:
			label = "ISBN"
		case 105:
			label = "Subject"
		case 106:
			label = "Published"
		case 503:
			label = "Title"
		case 524:
			label = "Language"
		default:
			continue
		}
		text := decodeMOBIText(data, encoding)
		if text != "" {
			out.WriteString(label)
			out.WriteString(": ")
			out.WriteString(text)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// decodeMOBIText returns a UTF-8 string for the given MOBI
// byte slice. MOBI has two legal encodings per the header
// text-encoding field: 1252 (Windows-1252) and 65001 (UTF-8).
// For 1252 we do a best-effort rune-by-rune widen; for UTF-8
// we pass through. Trims trailing nulls and whitespace.
func decodeMOBIText(b []byte, encoding uint32) string {
	b = bytes.TrimRight(b, "\x00 \t\r\n")
	if len(b) == 0 {
		return ""
	}
	switch encoding {
	case 65001:
		return string(b)
	case 1252, 0:
		// Windows-1252 is a superset of ISO-8859-1 for most
		// printable bytes; runes 0x80-0x9f have special
		// mappings. Best-effort: widen every byte to a rune.
		// Accurate enough for the metadata we care about
		// (titles, authors) which are usually ASCII anyway.
		out := make([]rune, 0, len(b))
		for _, c := range b {
			out = append(out, rune(c))
		}
		return string(out)
	}
	return string(b)
}

func init() {
	Register(NewMOBIExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 64*1024*1024 {
			return false
		}
		switch mime {
		case "application/x-mobipocket-ebook", "application/vnd.amazon.ebook":
			return true
		}
		lower := strings.ToLower(c.Path)
		return strings.HasSuffix(lower, ".mobi") ||
			strings.HasSuffix(lower, ".azw") ||
			strings.HasSuffix(lower, ".azw3")
	})
}
