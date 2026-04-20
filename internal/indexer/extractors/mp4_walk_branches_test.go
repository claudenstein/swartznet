package extractors

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// TestMP4ExtractorTooShort covers the `len(raw) < 8 → error` guard.
func TestMP4ExtractorTooShort(t *testing.T) {
	t.Parallel()
	_, err := NewMP4Extractor().Extract(bytes.NewReader([]byte{0, 1, 2}), 0)
	if err == nil {
		t.Error("expected error for too-short input")
	}
}

// TestMP4ExtractorNoMetadata covers the `text == "" → nil, nil`
// branch: an otherwise-valid file whose moov tree carries no
// recognised tags.
func TestMP4ExtractorNoMetadata(t *testing.T) {
	t.Parallel()
	ftyp := mp4Atom("ftyp", []byte("mp42\x00\x00\x00\x00mp42isom"))
	moov := mp4Atom("moov", mp4Atom("udta", []byte{}))
	chunks, err := NewMP4Extractor().Extract(bytes.NewReader(append(ftyp, moov...)), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestWalkMP4AtomsSizeZero covers the `size == 0 → extend-to-end`
// branch. An atom with declared size 0 should cover the rest of
// the buffer. We use a known container type (moov) whose body is
// empty so walkMP4Atoms recurses harmlessly.
func TestWalkMP4AtomsSizeZero(t *testing.T) {
	t.Parallel()
	// 8-byte atom header with size=0 meaning "to end of buffer".
	// moov is a known container; empty body is valid.
	atom := make([]byte, 8)
	binary.BigEndian.PutUint32(atom[0:4], 0) // size = 0
	copy(atom[4:8], "moov")

	var out strings.Builder
	walkMP4Atoms(atom, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// TestWalkMP4AtomsSize1Guard covers the `size == 1 && p+16 >
// len(b) → return` guard: a 64-bit-size marker with no room for
// the extended size.
func TestWalkMP4AtomsSize1Guard(t *testing.T) {
	t.Parallel()
	atom := make([]byte, 8)
	binary.BigEndian.PutUint32(atom[0:4], 1) // size marker for 64-bit
	copy(atom[4:8], "moov")
	// Only 8 bytes provided; the 64-bit extended size would need 16.

	var out strings.Builder
	walkMP4Atoms(atom, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// TestWalkMP4AtomsSize1With64BitSize covers the successful 64-bit
// size path — the reader must parse bytes 8..16 as the real size.
func TestWalkMP4AtomsSize1With64BitSize(t *testing.T) {
	t.Parallel()
	// Build: 4-byte size=1, 4-byte type=moov, 8-byte ext size=16
	// (covers just the header itself, no body).
	atom := make([]byte, 16)
	binary.BigEndian.PutUint32(atom[0:4], 1)
	copy(atom[4:8], "moov")
	binary.BigEndian.PutUint64(atom[8:16], 16)

	var out strings.Builder
	walkMP4Atoms(atom, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// TestWalkMP4AtomsTooSmallSize covers the `size < 8 → return`
// guard — protects against pathological atoms.
func TestWalkMP4AtomsTooSmallSize(t *testing.T) {
	t.Parallel()
	atom := make([]byte, 8)
	binary.BigEndian.PutUint32(atom[0:4], 4) // invalid, < 8
	copy(atom[4:8], "moov")

	var out strings.Builder
	walkMP4Atoms(atom, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// TestWalkMP4AtomsDepthGuard covers the `depth > 8 → return`
// recursion ceiling. Nest moov containers ten deep with a tag at
// the bottom; the guard must fire before we reach it.
func TestWalkMP4AtomsDepthGuard(t *testing.T) {
	t.Parallel()
	inner := mp4Atom("\xA9nam", mp4DataAtom("Deep Title"))
	// Wrap inner in 10 moov layers; only 8 levels of recursion are
	// allowed so the tag must not be emitted.
	payload := inner
	for i := 0; i < 10; i++ {
		payload = mp4Atom("moov", payload)
	}

	var out strings.Builder
	walkMP4Atoms(payload, &out, 0)
	if strings.Contains(out.String(), "Deep Title") {
		t.Errorf("depth guard failed: got %q", out.String())
	}
}

// TestWalkMP4AtomsMetaTooSmall covers the `meta body ≤ 4 → skip`
// guard: a meta atom whose body is smaller than the required
// version/flags prefix.
func TestWalkMP4AtomsMetaTooSmall(t *testing.T) {
	t.Parallel()
	// meta atom with a 2-byte body (less than the 4-byte v/flags
	// prefix) should not trigger a recursion.
	meta := mp4Atom("meta", []byte{0, 0})

	var out strings.Builder
	walkMP4Atoms(meta, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}
