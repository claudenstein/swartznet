package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// buildMinimalJPEGWithEXIF synthesises a tiny valid JPEG
// beginning with SOI, an APP1 EXIF segment containing a
// little-endian TIFF with two ASCII tags (camera make + model),
// then EOI.
func buildMinimalJPEGWithEXIF(cameraMake, cameraModel string) []byte {
	// TIFF: II*\0 + IFD offset(8) + IFD.
	// IFD: count(2) + N×12-byte entries + nextIFD(4).
	// We use inline storage for short strings and out-of-line
	// for longer ones. The test strings here are ≤4 chars so
	// they fit inline.
	// If they don't fit inline, we need the "store later and
	// reference by offset" path. Let's use short strings to
	// keep the test simple but still exercise the parser.
	var tiff bytes.Buffer

	// Header
	tiff.WriteString("II")
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x2A00)) // wait, should be 0x002A LE = bytes 2A,00
	// Actually II means LE, so write 0x002A as LE: 2A 00
	// We wrote 0x2A00 as LE which gives 00 2A — wrong. Fix:
	tiff.Reset()
	tiff.WriteString("II")
	tiff.Write([]byte{0x2A, 0x00})                          // magic
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8)) // IFD offset

	// Gather string data that doesn't fit inline. Entries are
	// added first with placeholder offsets, then real offsets
	// backpatched.
	type entry struct {
		tag uint16
		s   string
	}
	entries := []entry{{0x010F, cameraMake + "\x00"}, {0x0110, cameraModel + "\x00"}}

	// IFD: count (2 bytes) + entries + nextIFD (4)
	ifdSize := 2 + 12*len(entries) + 4
	afterIFD := 8 + ifdSize // offset where string data begins

	// Write count
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(len(entries)))

	// First pass: write entries, stash string bytes to append later.
	extraOff := afterIFD
	extras := make([]string, 0)
	for _, e := range entries {
		tag := e.tag
		typ := uint16(2) // ASCII
		cnt := uint32(len(e.s))
		_ = binary.Write(&tiff, binary.LittleEndian, tag)
		_ = binary.Write(&tiff, binary.LittleEndian, typ)
		_ = binary.Write(&tiff, binary.LittleEndian, cnt)
		if cnt <= 4 {
			// Inline: pad to 4 bytes.
			slot := make([]byte, 4)
			copy(slot, e.s)
			tiff.Write(slot)
		} else {
			_ = binary.Write(&tiff, binary.LittleEndian, uint32(extraOff))
			extras = append(extras, e.s)
			extraOff += len(e.s)
		}
	}
	// Next IFD offset = 0
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0))
	// Append out-of-line strings.
	for _, s := range extras {
		tiff.WriteString(s)
	}

	// APP1 EXIF segment body = "Exif\0\0" + TIFF
	var segBody bytes.Buffer
	segBody.WriteString("Exif\x00\x00")
	segBody.Write(tiff.Bytes())

	// Full JPEG: FF D8 FF E1 <len BE> <seg body> FF D9
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8}) // SOI
	out.Write([]byte{0xFF, 0xE1}) // APP1
	length := 2 + segBody.Len()   // length field + body
	_ = binary.Write(&out, binary.BigEndian, uint16(length))
	out.Write(segBody.Bytes())
	out.Write([]byte{0xFF, 0xD9}) // EOI
	return out.Bytes()
}

func TestEXIFExtractorReadsCameraMakeModel(t *testing.T) {
	t.Parallel()
	// Short values (<= 4 chars) fit inline in the TIFF entry.
	data := buildMinimalJPEGWithEXIF("Sny", "A7M")

	chunks, err := NewEXIFExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks — parser didn't find the APP1 segment")
	}
	text := chunks[0].Text
	if !strings.Contains(text, "Sny") {
		t.Errorf("missing make in:\n%s", text)
	}
	if !strings.Contains(text, "A7M") {
		t.Errorf("missing model in:\n%s", text)
	}
}

func TestEXIFExtractorRejectsNonJPEG(t *testing.T) {
	t.Parallel()
	_, err := NewEXIFExtractor().Extract(strings.NewReader("PNG data"), 0)
	if err == nil {
		t.Error("expected error for non-JPEG")
	}
}

func TestEXIFExtractorDispatchByExtension(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"photo.jpg", "photo.jpeg"} {
		e, _ := Dispatch(Candidate{Path: path, Size: 1024})
		if e == nil || e.Name() != "exif" {
			t.Errorf("%s: got extractor=%v, want exif", path, e)
		}
	}
}
