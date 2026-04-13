package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// buildID3v24Tag creates a minimal ID3v2.4 tag with the given
// frames. Each frame is {4-byte ID, size(syncsafe), flags(2),
// body}.
func buildID3v24Tag(frames map[string]string) []byte {
	// Build each frame body as: encoding=3 (UTF-8) + text.
	var framesBytes bytes.Buffer
	for id, text := range frames {
		body := append([]byte{3}, []byte(text)...)
		var frame bytes.Buffer
		frame.WriteString(id)
		// syncsafe size
		size := len(body)
		frame.Write([]byte{
			byte((size >> 21) & 0x7f),
			byte((size >> 14) & 0x7f),
			byte((size >> 7) & 0x7f),
			byte(size & 0x7f),
		})
		// flags (2 bytes, zero)
		frame.Write([]byte{0, 0})
		frame.Write(body)
		framesBytes.Write(frame.Bytes())
	}

	// Header: "ID3" + 2.4.0 + flags(0) + syncsafe size of frames
	var out bytes.Buffer
	out.WriteString("ID3")
	out.WriteByte(4) // major version
	out.WriteByte(0) // revision
	out.WriteByte(0) // flags
	tagSize := framesBytes.Len()
	out.Write([]byte{
		byte((tagSize >> 21) & 0x7f),
		byte((tagSize >> 14) & 0x7f),
		byte((tagSize >> 7) & 0x7f),
		byte(tagSize & 0x7f),
	})
	out.Write(framesBytes.Bytes())
	// Emulate the start of audio data by appending a byte of
	// junk; the extractor must NOT read past the tag.
	out.WriteByte(0xff)
	return out.Bytes()
}

func TestID3ExtractorV24(t *testing.T) {
	t.Parallel()
	data := buildID3v24Tag(map[string]string{
		"TIT2": "Song Title",
		"TPE1": "Artist Name",
		"TALB": "Album Name",
		"TDRC": "2024",
		"TCON": "Electronic",
	})

	chunks, err := NewID3Extractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	for _, want := range []string{
		"Title: Song Title",
		"Artist: Artist Name",
		"Album: Album Name",
		"Year: 2024",
		"Genre: Electronic",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestID3ExtractorV23(t *testing.T) {
	t.Parallel()
	// v2.3 uses big-endian non-syncsafe frame sizes.
	body := append([]byte{3}, []byte("V2.3 Title")...)

	var frames bytes.Buffer
	frames.WriteString("TIT2")
	_ = binary.Write(&frames, binary.BigEndian, uint32(len(body)))
	frames.Write([]byte{0, 0})
	frames.Write(body)

	var out bytes.Buffer
	out.WriteString("ID3")
	out.WriteByte(3) // version 2.3
	out.WriteByte(0)
	out.WriteByte(0)
	tagSize := frames.Len()
	out.Write([]byte{
		byte((tagSize >> 21) & 0x7f),
		byte((tagSize >> 14) & 0x7f),
		byte((tagSize >> 7) & 0x7f),
		byte(tagSize & 0x7f),
	})
	out.Write(frames.Bytes())

	chunks, err := NewID3Extractor().Extract(bytes.NewReader(out.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 || !strings.Contains(chunks[0].Text, "V2.3 Title") {
		t.Errorf("didn't parse v2.3 tag: %+v", chunks)
	}
}

func TestID3ExtractorRejectsJunk(t *testing.T) {
	t.Parallel()
	_, err := NewID3Extractor().Extract(strings.NewReader("not an mp3"), 0)
	if err == nil {
		t.Error("expected error for junk")
	}
}

func TestID3ExtractorDispatch(t *testing.T) {
	t.Parallel()
	e, _ := Dispatch(Candidate{Path: "song.mp3", Size: 1024})
	if e == nil || e.Name() != "id3" {
		t.Errorf("got extractor=%v, want id3", e)
	}
}
