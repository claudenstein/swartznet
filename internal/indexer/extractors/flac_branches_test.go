package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// flacWithVorbisBlock builds a minimal FLAC blob: signature
// followed by one VORBIS_COMMENT (block type 4) carrying the
// supplied body bytes.
func flacWithVorbisBlock(body []byte) []byte {
	var out bytes.Buffer
	out.WriteString("fLaC")
	ln := len(body)
	out.Write([]byte{0x84, byte(ln >> 16), byte(ln >> 8), byte(ln)}) // isLast=1, type=4
	out.Write(body)
	return out.Bytes()
}

// TestParseVorbisCommentLenLessThanFour covers the
// `if len(b) < 4 { return "" }` arm. A vorbis body of fewer than
// 4 bytes never appears in real FLAC, but the parser is exposed
// directly to fuzzy data and must defend itself.
func TestParseVorbisCommentLenLessThanFour(t *testing.T) {
	t.Parallel()
	if got := parseVorbisComment([]byte{0x01, 0x02}); got != "" {
		t.Errorf("parseVorbisComment(short) = %q, want empty", got)
	}
}

// TestParseVorbisCommentVendorOverrun covers the
// `if p+4 > len(b) { return "" }` arm: vendor length claims
// more bytes than the remaining body holds. Parser bails out
// rather than indexing past the end of the slice.
func TestParseVorbisCommentVendorOverrun(t *testing.T) {
	t.Parallel()
	var body bytes.Buffer
	_ = binary.Write(&body, binary.LittleEndian, uint32(100)) // claim 100-byte vendor
	body.Write([]byte("only-a-few-bytes"))                    // < 100
	if got := parseVorbisComment(body.Bytes()); got != "" {
		t.Errorf("parseVorbisComment(vendor overrun) = %q, want empty", got)
	}
}

// TestParseVorbisCommentInvalidLength covers the
// `if ln < 0 || p+ln > len(b) { break }` arm: a comment field
// claims more bytes than remain.
func TestParseVorbisCommentInvalidLength(t *testing.T) {
	t.Parallel()
	var body bytes.Buffer
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))  // empty vendor
	_ = binary.Write(&body, binary.LittleEndian, uint32(1))  // 1 comment claimed
	_ = binary.Write(&body, binary.LittleEndian, uint32(99)) // claim 99-byte field
	body.Write([]byte("short"))                              // <99 bytes
	if got := parseVorbisComment(body.Bytes()); got != "" {
		t.Errorf("parseVorbisComment(invalid len) = %q, want empty", got)
	}
}

// TestParseVorbisCommentVariousFields exercises the per-field
// branches: skips entries without '=' (118-119), skips empty
// values (123-124), skips unknown labels (127-128), and
// surfaces a known label.
func TestParseVorbisCommentVariousFields(t *testing.T) {
	t.Parallel()
	fields := []string{
		"NO_EQUALS_SIGN",                // hits the eq < 0 continue
		"EMPTY=",                        // empty value
		"UNRECOGNIZED=does not surface", // vorbisLabel returns ""
		"TITLE=My Track",                // surfaces
	}
	body := buildVorbisBody("vendor", fields)
	got := parseVorbisComment(body)
	if !strings.Contains(got, "Title: My Track") {
		t.Errorf("expected Title field, got %q", got)
	}
	if strings.Contains(got, "UNRECOGNIZED") || strings.Contains(got, "EMPTY") || strings.Contains(got, "NO_EQUALS_SIGN") {
		t.Errorf("non-recognized fields leaked into output: %q", got)
	}
}

// TestFLACExtractorVorbisBodyTruncated covers the
// `_, err := readFull(br, bodyLen) ... err != nil` arm. The
// block header declares a 100-byte vorbis body but only 5 bytes
// follow, so readFull returns ErrUnexpectedEOF.
func TestFLACExtractorVorbisBodyTruncated(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	out.WriteString("fLaC")
	out.Write([]byte{0x84, 0x00, 0x00, 0x64}) // isLast=1, type=4, bodyLen=100
	out.Write([]byte("short"))                // only 5 bytes follow

	if _, err := NewFLACExtractor().Extract(bytes.NewReader(out.Bytes()), 0); err == nil {
		t.Error("expected error on truncated vorbis body")
	}
}

// TestFLACExtractorDiscardError covers the
// `if _, err := br.Discard(bodyLen); err != nil` arm. Declare
// a non-VORBIS block whose body is bigger than what follows so
// br.Discard runs out of bytes.
func TestFLACExtractorDiscardError(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	out.WriteString("fLaC")
	// blockType=0 (STREAMINFO), bodyLen=100, isLast=0
	out.Write([]byte{0x00, 0x00, 0x00, 0x64})
	out.Write([]byte("short")) // <100 bytes

	if _, err := NewFLACExtractor().Extract(bytes.NewReader(out.Bytes()), 0); err == nil {
		t.Error("expected error on truncated discard")
	}
}

// buildVorbisBody assembles a Vorbis comment body with the
// given vendor + comment list.
func buildVorbisBody(vendor string, comments []string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(vendor)))
	b.WriteString(vendor)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(comments)))
	for _, c := range comments {
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(c)))
		b.WriteString(c)
	}
	return b.Bytes()
}

// satisfy import.
var _ = flacWithVorbisBlock
