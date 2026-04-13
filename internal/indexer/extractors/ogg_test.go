package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// buildOGGVorbisPacket wraps a Vorbis comment packet in a
// minimal Ogg page. We skip the initial ID header and just emit
// the comment-type packet in a single page.
func buildOGGVorbisPacket(comments map[string]string) []byte {
	// Vorbis comment packet body: type 0x03 + "vorbis" +
	// vendor_len + vendor + comment_count + comments.
	var packet bytes.Buffer
	packet.WriteByte(0x03)
	packet.WriteString("vorbis")

	vendor := "swartznet-test"
	_ = binary.Write(&packet, binary.LittleEndian, uint32(len(vendor)))
	packet.WriteString(vendor)
	_ = binary.Write(&packet, binary.LittleEndian, uint32(len(comments)))
	for k, v := range comments {
		entry := k + "=" + v
		_ = binary.Write(&packet, binary.LittleEndian, uint32(len(entry)))
		packet.WriteString(entry)
	}

	// Wrap in a single Ogg page. Segment table has ceil(len/255)
	// entries, each up to 255 bytes.
	data := packet.Bytes()
	var segTable bytes.Buffer
	remaining := len(data)
	for remaining > 0 {
		if remaining >= 255 {
			segTable.WriteByte(255)
			remaining -= 255
		} else {
			segTable.WriteByte(byte(remaining))
			remaining = 0
		}
	}
	// If last segment was exactly 255, add a 0 to signal end.
	if len(data)%255 == 0 && len(data) > 0 {
		segTable.WriteByte(0)
	}

	var out bytes.Buffer
	out.WriteString("OggS")
	out.WriteByte(0)           // version
	out.WriteByte(0)           // header type
	out.Write(make([]byte, 8)) // granule position
	out.Write(make([]byte, 4)) // stream serial
	out.Write(make([]byte, 4)) // page sequence
	out.Write(make([]byte, 4)) // checksum
	out.WriteByte(byte(segTable.Len()))
	out.Write(segTable.Bytes())
	out.Write(data)
	return out.Bytes()
}

func TestOGGExtractorVorbisComments(t *testing.T) {
	t.Parallel()
	data := buildOGGVorbisPacket(map[string]string{
		"TITLE":  "Symphony No. 9",
		"ARTIST": "Ludwig van Beethoven",
		"DATE":   "1824",
	})

	chunks, err := NewOGGExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	text := chunks[0].Text
	for _, want := range []string{
		"Title: Symphony No. 9",
		"Artist: Ludwig van Beethoven",
		"Date: 1824",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestOGGExtractorDispatch(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"song.ogg", "audio.oga", "voice.opus"} {
		e, _ := Dispatch(Candidate{Path: path, Size: 1024})
		if e == nil || e.Name() != "ogg" {
			t.Errorf("%s: got %v, want ogg", path, e)
		}
	}
}
