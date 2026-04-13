package extractors

import (
	"bytes"
	"strings"
	"testing"
)

// ebmlID encodes an EBML element ID back to bytes with the
// leading-bit length marker preserved — matches what real MKV
// files carry on disk. We only use IDs whose lengths we know
// in this test.
func ebmlID(id uint32) []byte {
	// IDs in our test range are 1-4 bytes. Find the leading
	// non-zero byte, then emit the full chain.
	var out []byte
	for i := 3; i >= 0; i-- {
		b := byte(id >> (i * 8))
		if len(out) > 0 || b != 0 {
			out = append(out, b)
		}
	}
	return out
}

// ebmlSize encodes a size as a VINT, stripping the marker bit
// from the first byte (matches what parseVINT expects for
// sizes).
func ebmlSize(n int) []byte {
	if n < 0x80 {
		return []byte{0x80 | byte(n)}
	}
	if n < 0x4000 {
		return []byte{0x40 | byte(n>>8), byte(n)}
	}
	if n < 0x200000 {
		return []byte{0x20 | byte(n>>16), byte(n >> 8), byte(n)}
	}
	return []byte{0x10 | byte(n>>24), byte(n >> 16), byte(n >> 8), byte(n)}
}

// ebmlElem wraps a body with its id + size.
func ebmlElem(id uint32, body []byte) []byte {
	var out bytes.Buffer
	out.Write(ebmlID(id))
	out.Write(ebmlSize(len(body)))
	out.Write(body)
	return out.Bytes()
}

func TestMKVExtractorBasic(t *testing.T) {
	t.Parallel()

	// Build a minimal MKV file: EBML header + Segment containing
	// an Info block with Title + a Chapters block with one
	// chapter atom containing a ChapString.
	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))

	info := ebmlElem(ebmlIDInfo, ebmlElem(ebmlIDTitle, []byte("The Movie Title")))

	chapStr := ebmlElem(ebmlIDChapString, []byte("Opening credits"))
	chapDisplay := ebmlElem(ebmlIDChapDisplay, chapStr)
	chapAtom := ebmlElem(ebmlIDChapterAtom, chapDisplay)
	chapters := ebmlElem(ebmlIDChapters, chapAtom)

	segment := ebmlElem(ebmlIDSegment, append(info, chapters...))

	file := append(ebmlHeader, segment...)

	chunks, err := NewMKVExtractor().Extract(bytes.NewReader(file), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	if !strings.Contains(text, "Title: The Movie Title") {
		t.Errorf("missing title in:\n%s", text)
	}
	if !strings.Contains(text, "Chapter: Opening credits") {
		t.Errorf("missing chapter in:\n%s", text)
	}
}

func TestMKVExtractorRejectsNonEBML(t *testing.T) {
	t.Parallel()
	_, err := NewMKVExtractor().Extract(strings.NewReader("not an mkv file"), 0)
	if err == nil {
		t.Error("expected error")
	}
}

func TestMKVExtractorDispatch(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"video.mkv", "clip.webm", "audio.mka"} {
		e, _ := Dispatch(Candidate{Path: path, Size: 1024})
		if e == nil || e.Name() != "mkv" {
			t.Errorf("%s: got %v, want mkv", path, e)
		}
	}
}
