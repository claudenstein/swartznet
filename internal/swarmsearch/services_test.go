package swarmsearch

import (
	"testing"
)

// TestServiceBitsRoundTrip covers the conversion between the
// legacy Capabilities struct and the M15b ServiceBits bitfield.
// The conversion must be lossless in both directions for the
// legacy 4-field subset.
func TestServiceBitsRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []Capabilities{
		{ShareLocal: 2, FileHits: 1, ContentHits: 1, Publisher: 0}, // default
		{ShareLocal: 2, FileHits: 1, ContentHits: 1, Publisher: 1},
		{ShareLocal: 1, FileHits: 1, ContentHits: 0, Publisher: 0},
		{ShareLocal: 0, FileHits: 0, ContentHits: 0, Publisher: 0}, // silent
	}
	for _, c := range cases {
		bits := ServicesFromCapabilities(c)
		got := bits.ToCapabilities()
		if got != c {
			t.Errorf("round trip lost data: %+v → %064b → %+v", c, bits, got)
		}
	}
}

// TestServiceBitsUnknownBitsIgnored is the critical invariant:
// unknown bits in the upper range must NOT prevent correct
// parsing of known bits. This property is what makes feature
// addition additive forever.
func TestServiceBitsUnknownBitsIgnored(t *testing.T) {
	t.Parallel()
	// A future peer might set bits 50-55 for features this
	// build doesn't know about. The legacy cap extraction must
	// still correctly surface the known bits.
	future := DefaultServices() | (1 << 50) | (1 << 51) | (1 << 55)
	caps := future.ToCapabilities()
	want := DefaultServices().ToCapabilities()
	if caps != want {
		t.Errorf("unknown bits affected cap extraction: got %+v want %+v", caps, want)
	}
}

// TestServiceBitsHelpers exercises Has / With / Without.
func TestServiceBitsHelpers(t *testing.T) {
	t.Parallel()
	var s ServiceBits
	if s.Has(BitShareLocal) {
		t.Errorf("zero mask has no bits")
	}
	s = s.With(BitShareLocal).With(BitFileHits)
	if !s.Has(BitShareLocal) || !s.Has(BitFileHits) {
		t.Errorf("With didn't set bits: %064b", s)
	}
	if s.Has(BitContentHits) {
		t.Errorf("With set unrelated bit")
	}
	s = s.Without(BitShareLocal)
	if s.Has(BitShareLocal) {
		t.Errorf("Without didn't clear")
	}
	if !s.Has(BitFileHits) {
		t.Errorf("Without cleared unrelated bit")
	}
}

// TestDefaultServicesMatchesDefaultCapabilities: the default
// service mask must surface exactly the same legacy cap struct
// as DefaultCapabilities. This is the bridge that lets existing
// callers continue using Capabilities while new callers adopt
// ServiceBits.
func TestDefaultServicesMatchesDefaultCapabilities(t *testing.T) {
	t.Parallel()
	got := DefaultServices().ToCapabilities()
	want := DefaultCapabilities()
	if got != want {
		t.Errorf("DefaultServices().ToCapabilities() = %+v, want %+v", got, want)
	}
}
