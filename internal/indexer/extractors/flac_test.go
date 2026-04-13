package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// buildFLAC synthesises a tiny FLAC file with a STREAMINFO
// block (required first) and a VORBIS_COMMENT block.
func buildFLAC(comments map[string]string) []byte {
	var out bytes.Buffer
	out.WriteString("fLaC")

	// Block 1: STREAMINFO, body of 34 bytes (we don't care
	// about its contents). isLast=0, type=0.
	out.Write([]byte{0x00, 0x00, 0x00, 0x22})
	out.Write(make([]byte, 34))

	// Build the Vorbis comment body: vendor_len + vendor +
	// count + N × (len + "NAME=value").
	var body bytes.Buffer
	vendor := "swartznet test"
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(vendor)))
	body.WriteString(vendor)
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(comments)))
	for k, v := range comments {
		entry := k + "=" + v
		_ = binary.Write(&body, binary.LittleEndian, uint32(len(entry)))
		body.WriteString(entry)
	}

	// Block 2: VORBIS_COMMENT (type 4), isLast=1.
	ln := body.Len()
	hdr := []byte{
		0x84, // 1000 0100 = last-block + type 4
		byte(ln >> 16),
		byte(ln >> 8),
		byte(ln),
	}
	out.Write(hdr)
	out.Write(body.Bytes())
	return out.Bytes()
}

func TestFLACExtractorBasic(t *testing.T) {
	t.Parallel()
	data := buildFLAC(map[string]string{
		"TITLE":  "Clair de Lune",
		"ARTIST": "Claude Debussy",
		"ALBUM":  "Suite Bergamasque",
		"DATE":   "1905",
		"GENRE":  "Classical",
	})
	chunks, err := NewFLACExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	for _, want := range []string{
		"Title: Clair de Lune",
		"Artist: Claude Debussy",
		"Album: Suite Bergamasque",
		"Date: 1905",
		"Genre: Classical",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestFLACExtractorRejectsNonFLAC(t *testing.T) {
	t.Parallel()
	_, err := NewFLACExtractor().Extract(strings.NewReader("not flac"), 0)
	if err == nil {
		t.Error("expected error")
	}
}

func TestFLACExtractorDispatch(t *testing.T) {
	t.Parallel()
	e, _ := Dispatch(Candidate{Path: "song.flac", Size: 1024})
	if e == nil || e.Name() != "flac" {
		t.Errorf("got %v, want flac", e)
	}
}
