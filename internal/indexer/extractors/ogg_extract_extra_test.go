package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// wrapOGGPage wraps an arbitrary packet payload in a single
// minimal Ogg page (mirrors buildOGGVorbisPacket but takes the
// already-assembled packet bytes directly so we can synthesise
// Opus pages and empty-comment packets without duplication).
func wrapOGGPage(packet []byte) []byte {
	var segTable bytes.Buffer
	remaining := len(packet)
	for remaining > 0 {
		if remaining >= 255 {
			segTable.WriteByte(255)
			remaining -= 255
		} else {
			segTable.WriteByte(byte(remaining))
			remaining = 0
		}
	}
	if len(packet)%255 == 0 && len(packet) > 0 {
		segTable.WriteByte(0)
	}

	var out bytes.Buffer
	out.WriteString("OggS")
	out.WriteByte(0)
	out.WriteByte(0)
	out.Write(make([]byte, 8))
	out.Write(make([]byte, 4))
	out.Write(make([]byte, 4))
	out.Write(make([]byte, 4))
	out.WriteByte(byte(segTable.Len()))
	out.Write(segTable.Bytes())
	out.Write(packet)
	return out.Bytes()
}

// TestOGGExtractorOpusPath covers the OpusTags branch in Extract.
func TestOGGExtractorOpusPath(t *testing.T) {
	t.Parallel()
	var packet bytes.Buffer
	packet.WriteString("OpusTags")
	vendor := "swartznet-opus"
	_ = binary.Write(&packet, binary.LittleEndian, uint32(len(vendor)))
	packet.WriteString(vendor)
	_ = binary.Write(&packet, binary.LittleEndian, uint32(1))
	entry := "TITLE=Opus Sonata"
	_ = binary.Write(&packet, binary.LittleEndian, uint32(len(entry)))
	packet.WriteString(entry)

	data := wrapOGGPage(packet.Bytes())
	chunks, err := NewOGGExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 || !strings.Contains(chunks[0].Text, "Opus Sonata") {
		t.Fatalf("missing Opus title, got %v", chunks)
	}
}

// TestOGGExtractorEmptyVorbisComments covers the "text == \"\"
// → return nil, nil" branch in the Vorbis arm.
func TestOGGExtractorEmptyVorbisComments(t *testing.T) {
	t.Parallel()
	var packet bytes.Buffer
	packet.WriteByte(0x03)
	packet.WriteString("vorbis")
	// vendor_len=0, vendor="", comment_count=0 → parseVorbisComment
	// returns "" and Extract should produce no chunks.
	_ = binary.Write(&packet, binary.LittleEndian, uint32(0))
	_ = binary.Write(&packet, binary.LittleEndian, uint32(0))

	data := wrapOGGPage(packet.Bytes())
	chunks, err := NewOGGExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestOGGExtractorEmptyOpusComments covers the same empty-text
// branch on the Opus arm.
func TestOGGExtractorEmptyOpusComments(t *testing.T) {
	t.Parallel()
	var packet bytes.Buffer
	packet.WriteString("OpusTags")
	_ = binary.Write(&packet, binary.LittleEndian, uint32(0))
	_ = binary.Write(&packet, binary.LittleEndian, uint32(0))

	data := wrapOGGPage(packet.Bytes())
	chunks, err := NewOGGExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestOGGExtractorPageReadError covers the non-EOF error branch —
// readOGGPage returns "bad capture pattern" which Extract wraps
// and propagates.
func TestOGGExtractorPageReadError(t *testing.T) {
	t.Parallel()
	// 27-byte header not starting with "OggS". readOGGPage reads
	// all 27 bytes successfully then rejects the capture pattern.
	hdr := make([]byte, 27)
	copy(hdr, "NotAnOggStreamHeader....")
	_, err := NewOGGExtractor().Extract(bytes.NewReader(hdr), 0)
	if err == nil {
		t.Error("expected non-EOF read error to propagate")
	}
}
