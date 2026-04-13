package extractors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// EXIFExtractor reads metadata from JPEG image files via the
// APP1 EXIF segment (the most common source of camera/time
// metadata). We extract a small, user-useful subset — camera
// make/model, datetime taken, image description, software,
// artist, copyright, and GPS coordinates if present.
//
// JPEG container: starts with 0xFF 0xD8 (SOI), then a sequence
// of segments, each tagged 0xFF <marker> and carrying a
// 2-byte big-endian length (includes the length field itself
// but not the 0xFF marker). APP1 (0xFFE1) with the "Exif\0\0"
// signature contains the EXIF TIFF payload.
//
// EXIF TIFF layout:
//
//	0-1   byte order: "II" = little-endian, "MM" = big-endian
//	2-3   magic (0x002A / 0x2A00)
//	4-7   offset to first IFD (from the start of this TIFF hdr)
//	IFD:
//	  number of entries (uint16)
//	  12-byte entries: tag(2) + type(2) + count(4) + value/offset(4)
//	  next-IFD offset (uint32, 0 = no more)
//
// A single-value entry with ≤4 bytes of data stores the data
// inline in the value/offset slot; otherwise that slot points
// to data elsewhere in the TIFF block.
type EXIFExtractor struct{}

// NewEXIFExtractor returns a ready-to-use EXIF extractor.
func NewEXIFExtractor() *EXIFExtractor { return &EXIFExtractor{} }

// Name implements Extractor.
func (*EXIFExtractor) Name() string { return "exif" }

// Extract implements Extractor.
func (e *EXIFExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024 * 1024
	}

	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("exif: panic during extraction: %v", rec)
		}
	}()

	// We only need the first ~64 KiB to find the EXIF segment.
	// Reading the whole file would be wasteful for big photos.
	buf, err := io.ReadAll(io.LimitReader(r, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("exif: read: %w", err)
	}
	if len(buf) < 4 || buf[0] != 0xFF || buf[1] != 0xD8 {
		return nil, fmt.Errorf("exif: not a JPEG")
	}

	tiff, found := findExifTIFF(buf[2:])
	if !found {
		return nil, nil
	}

	entries, err := parseTIFF(tiff)
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	for _, k := range exifTagOrder {
		if v, ok := entries[k.tag]; ok && v != "" {
			out.WriteString(k.label)
			out.WriteString(": ")
			out.WriteString(v)
			out.WriteByte('\n')
		}
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil, nil
	}
	return []Chunk{{Text: text}}, nil
}

// findExifTIFF scans JPEG markers looking for an APP1 segment
// whose first 6 bytes are "Exif\0\0". Returns the TIFF block
// immediately after that signature.
func findExifTIFF(b []byte) ([]byte, bool) {
	i := 0
	for i < len(b)-1 {
		if b[i] != 0xFF {
			return nil, false
		}
		marker := b[i+1]
		i += 2
		// SOS (start of scan) and beyond: image data follows,
		// no more APP segments to find.
		if marker == 0xDA {
			return nil, false
		}
		if marker == 0 || marker == 0xFF {
			continue
		}
		if i+2 > len(b) {
			return nil, false
		}
		segLen := int(binary.BigEndian.Uint16(b[i : i+2]))
		if segLen < 2 {
			return nil, false
		}
		bodyStart := i + 2
		bodyEnd := i + segLen
		if bodyEnd > len(b) {
			return nil, false
		}
		body := b[bodyStart:bodyEnd]
		// APP1 EXIF marker.
		if marker == 0xE1 && len(body) >= 6 && string(body[:6]) == "Exif\x00\x00" {
			return body[6:], true
		}
		i = bodyEnd
	}
	return nil, false
}

// exifTagOrder is the fixed order we emit tags in, and the
// label we use for each.
var exifTagOrder = []struct {
	tag   uint16
	label string
}{
	{0x010F, "Camera make"},
	{0x0110, "Camera model"},
	{0x0131, "Software"},
	{0x013B, "Artist"},
	{0x010E, "Image description"},
	{0x8298, "Copyright"},
	{0x9003, "Date taken"},
	{0x9004, "Date digitised"},
	{0x0132, "Date"},
	// GPS latitude/longitude live in a nested IFD; we surface
	// them as composite "GPS" when both are available.
	{0x8825, "GPS"},
}

// parseTIFF walks the TIFF block and returns a flat
// tag → stringified-value map. GPS IFD (tag 0x8825 in IFD0)
// is expanded into a single "GPS" entry of the form
// "LAT,LON" if both coordinates are present.
func parseTIFF(t []byte) (map[uint16]string, error) {
	out := make(map[uint16]string)
	if len(t) < 8 {
		return nil, fmt.Errorf("exif: tiff too short")
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, fmt.Errorf("exif: bad byte-order marker")
	}
	ifdOffset := int(bo.Uint32(t[4:8]))
	if ifdOffset >= len(t) {
		return nil, fmt.Errorf("exif: IFD offset out of range")
	}
	gpsIFDOffset := parseIFD(t, ifdOffset, bo, out)
	if gpsIFDOffset > 0 && gpsIFDOffset < len(t) {
		gps := make(map[uint16]string)
		parseIFD(t, gpsIFDOffset, bo, gps)
		lat := gps[0x0002]
		latRef := gps[0x0001]
		lon := gps[0x0004]
		lonRef := gps[0x0003]
		if lat != "" && lon != "" {
			out[0x8825] = latRef + lat + ", " + lonRef + lon
		}
	}
	return out, nil
}

// parseIFD parses a single IFD and stores tag→string in out.
// Returns the offset to the GPS IFD pointer if tag 0x8825 is
// encountered, else 0.
func parseIFD(t []byte, offset int, bo binary.ByteOrder, out map[uint16]string) int {
	if offset+2 > len(t) {
		return 0
	}
	count := int(bo.Uint16(t[offset : offset+2]))
	p := offset + 2
	var gpsIFDOffset int
	for i := 0; i < count && p+12 <= len(t); i++ {
		tag := bo.Uint16(t[p : p+2])
		typ := bo.Uint16(t[p+2 : p+4])
		cnt := int(bo.Uint32(t[p+4 : p+8]))
		valSlot := t[p+8 : p+12]
		p += 12

		if tag == 0x8825 { // GPS IFD pointer
			gpsIFDOffset = int(bo.Uint32(valSlot))
			continue
		}

		// We only decode ASCII strings (type 2) and RATIONAL
		// (type 5) for GPS lat/lon.
		switch typ {
		case 2: // ASCII
			data := readValueBytes(t, valSlot, cnt, bo)
			s := strings.TrimRight(string(data), "\x00 \t\r\n")
			if s != "" {
				out[tag] = s
			}
		case 5: // RATIONAL — 3 for GPS lat/lon (D,M,S)
			data := readValueBytes(t, valSlot, cnt*8, bo)
			if len(data) >= 24 && cnt >= 3 {
				d := rational(data[0:8], bo)
				m := rational(data[8:16], bo)
				s := rational(data[16:24], bo)
				out[tag] = fmt.Sprintf("%.4f", d+m/60+s/3600)
			} else if len(data) >= 8 && cnt == 1 {
				out[tag] = fmt.Sprintf("%.4f", rational(data[:8], bo))
			}
		case 1, 7: // BYTE, UNDEFINED — usually reference strings ("N","E")
			if cnt <= 4 {
				out[tag] = strings.TrimSpace(string(bytes.TrimRight(valSlot[:cnt], "\x00")))
			}
		}
	}
	return gpsIFDOffset
}

// readValueBytes returns a tag's data bytes. If cnt ≤ 4, data
// is inline in the value slot. Otherwise the slot is a uint32
// offset into t.
func readValueBytes(t []byte, valSlot []byte, size int, bo binary.ByteOrder) []byte {
	if size <= 4 {
		return valSlot[:size]
	}
	off := int(bo.Uint32(valSlot))
	if off < 0 || off+size > len(t) {
		return nil
	}
	return t[off : off+size]
}

// rational decodes an 8-byte numerator/denominator rational
// into a float64.
func rational(b []byte, bo binary.ByteOrder) float64 {
	num := bo.Uint32(b[0:4])
	den := bo.Uint32(b[4:8])
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func init() {
	Register(NewEXIFExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 100*1024*1024 {
			return false
		}
		if mime == "image/jpeg" || mime == "image/jpg" {
			return true
		}
		lower := strings.ToLower(c.Path)
		return strings.HasSuffix(lower, ".jpg") ||
			strings.HasSuffix(lower, ".jpeg")
	})
}
