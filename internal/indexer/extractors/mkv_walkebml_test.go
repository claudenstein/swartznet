package extractors

import (
	"strings"
	"testing"
)

// TestWalkEBMLDepthGuard covers the `depth > 8 → return` ceiling.
// We fabricate a single TrackName element and claim we're already
// past the ceiling — walkEBML must refuse to descend.
func TestWalkEBMLDepthGuard(t *testing.T) {
	t.Parallel()
	body := ebmlElem(ebmlIDTrackName, []byte("Should Not Appear"))
	var out strings.Builder
	walkEBML(body, &out, 9)
	if strings.Contains(out.String(), "Should Not Appear") {
		t.Errorf("depth guard failed: %q", out.String())
	}
}

// TestWalkEBMLBadIDVINT covers the `idLen == 0 → return` branch —
// a byte slice whose leading byte is zero makes parseVINT reject
// the id.
func TestWalkEBMLBadIDVINT(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	walkEBML([]byte{0x00, 0x81, 'x'}, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// TestWalkEBMLBadSizeVINT covers the `sizeLen == 0 → return`
// branch — a valid 1-byte id followed by a zero size byte.
func TestWalkEBMLBadSizeVINT(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	// 0xA0 = valid 1-byte id; 0x00 = invalid size VINT.
	walkEBML([]byte{0xA0, 0x00}, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// TestWalkEBMLDataOverrun covers the `dataEnd > len(b) → return`
// guard — size claims more bytes than are available.
func TestWalkEBMLDataOverrun(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	// id 0xA0 (1 byte), size 0x83 (declares 3 bytes of body) but
	// only 1 body byte is present → overrun guard must fire.
	walkEBML([]byte{0xA0, 0x83, 'x'}, &out, 0)
	if out.Len() != 0 {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// TestWalkEBMLTrackEntryContainer covers the container-recurse
// branch for TrackEntry specifically. Nested TrackName must be
// emitted.
func TestWalkEBMLTrackEntryContainer(t *testing.T) {
	t.Parallel()
	inner := ebmlElem(ebmlIDTrackName, []byte("PrimaryAudio"))
	entry := ebmlElem(ebmlIDTrackEntry, inner)
	var out strings.Builder
	walkEBML(entry, &out, 0)
	if !strings.Contains(out.String(), "Track: PrimaryAudio") {
		t.Errorf("missing nested TrackName: %q", out.String())
	}
}

// TestWalkEBMLSimpleTagRecurses covers the SimpleTag container
// branch and also the TagName/TagString no-op arms (which exist
// but do not emit directly).
func TestWalkEBMLSimpleTagRecurses(t *testing.T) {
	t.Parallel()
	name := ebmlElem(ebmlIDTagName, []byte("ARTIST"))
	str := ebmlElem(ebmlIDTagString, []byte("Test Artist"))
	simple := ebmlElem(ebmlIDSimpleTag, append(name, str...))
	var out strings.Builder
	walkEBML(simple, &out, 0)
	// TagName and TagString are no-op in walkEBML, so nothing is
	// emitted. The branch is still exercised.
	if strings.Contains(out.String(), "Title:") {
		t.Errorf("unexpected Title emit: %q", out.String())
	}
}

// TestWalkEBMLEmitAllLeafTypes covers every leaf id → emit() branch
// that the standard MKV metadata surfaces.
func TestWalkEBMLEmitAllLeafTypes(t *testing.T) {
	t.Parallel()
	body := append([]byte{}, ebmlElem(ebmlIDMuxingApp, []byte("libavformat"))...)
	body = append(body, ebmlElem(ebmlIDWritingApp, []byte("swartznet"))...)
	body = append(body, ebmlElem(ebmlIDLanguage, []byte("eng"))...)

	var out strings.Builder
	walkEBML(body, &out, 0)
	got := out.String()
	for _, want := range []string{
		"Muxer: libavformat",
		"Writer: swartznet",
		"Language: eng",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %q", want, got)
		}
	}
}

// TestEmitTrimsTrailingNulls covers the null-byte trim + empty-
// value skip path of emit().
func TestEmitTrimsTrailingNulls(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	emit(&out, "Label", "value\x00\x00")
	if out.String() != "Label: value\n" {
		t.Errorf("emit: got %q, want 'Label: value\\n'", out.String())
	}
	// Empty after trim → no-op.
	var empty strings.Builder
	emit(&empty, "Label", "\x00\x00\x00")
	if empty.Len() != 0 {
		t.Errorf("empty emit produced: %q", empty.String())
	}
}
