package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// mp4Atom builds an atom with the given type and body.
func mp4Atom(atomType string, body []byte) []byte {
	size := 8 + len(body)
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[0:4], uint32(size))
	copy(hdr[4:8], atomType)
	return append(hdr, body...)
}

// mp4DataAtom builds a "data" sub-atom with UTF-8 text.
func mp4DataAtom(text string) []byte {
	// 16-byte header (8 size+type + 1 version + 3 flags + 4 reserved)
	body := make([]byte, 8+len(text))
	binary.BigEndian.PutUint32(body[0:4], 1) // flags = UTF-8
	// body[4:8] = reserved (0)
	copy(body[8:], text)
	return mp4Atom("data", body)
}

func TestMP4ExtractorBasic(t *testing.T) {
	t.Parallel()

	// Build: ftyp + moov/udta/meta/ilst/{©nam, ©ART, ©alb, ©day}
	ftyp := mp4Atom("ftyp", []byte("mp42\x00\x00\x00\x00mp42isom"))

	// iTunes tags (inside ilst)
	tagTitle := mp4Atom("\xA9nam", mp4DataAtom("The Title"))
	tagArtist := mp4Atom("\xA9ART", mp4DataAtom("The Artist"))
	tagAlbum := mp4Atom("\xA9alb", mp4DataAtom("The Album"))
	tagDay := mp4Atom("\xA9day", mp4DataAtom("2024"))
	ilst := mp4Atom("ilst", append(append(append(tagTitle, tagArtist...), tagAlbum...), tagDay...))

	// meta: 4-byte version/flags + children
	metaPayload := append([]byte{0, 0, 0, 0}, ilst...)
	meta := mp4Atom("meta", metaPayload)
	udta := mp4Atom("udta", meta)
	moov := mp4Atom("moov", udta)

	file := append(ftyp, moov...)

	chunks, err := NewMP4Extractor().Extract(bytes.NewReader(file), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	for _, want := range []string{
		"Title: The Title",
		"Artist: The Artist",
		"Album: The Album",
		"Date: 2024",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestMP4ExtractorRejectsNonMP4(t *testing.T) {
	t.Parallel()
	_, err := NewMP4Extractor().Extract(strings.NewReader("not an mp4 at all"), 0)
	if err == nil {
		t.Error("expected error")
	}
}

func TestMP4ExtractorDispatch(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"video.mp4", "song.m4a", "audiobook.m4b"} {
		e, _ := Dispatch(Candidate{Path: path, Size: 1024})
		if e == nil || e.Name() != "mp4" {
			t.Errorf("%s: got %v, want mp4", path, e)
		}
	}
}
