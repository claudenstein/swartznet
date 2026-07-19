package ltepwire_test

import (
	"testing"

	"github.com/swartznet/swartznet/contracts/ltepwire"
)

// TestBitConstantsFrozen pins the exact numeric value of every defined bit.
// These are wire targets — if any value changes, existing peers negotiate the
// wrong capability. This test is the freeze.
func TestBitConstantsFrozen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  ltepwire.ServiceBits
		want ltepwire.ServiceBits
	}{
		{"ShareLocal", ltepwire.BitShareLocal, 0x001},
		{"ShareSwarm", ltepwire.BitShareSwarm, 0x002},
		{"FileHits", ltepwire.BitFileHits, 0x004},
		{"ContentHits", ltepwire.BitContentHits, 0x008},
		{"LayerDPublisher", ltepwire.BitLayerDPublisher, 0x010},
		{"CompanionPublisher", ltepwire.BitCompanionPublisher, 0x020},
		{"CompanionSubscriber", ltepwire.BitCompanionSubscriber, 0x040},
		{"SnippetHighlight", ltepwire.BitSnippetHighlight, 0x080},
		{"Regtest", ltepwire.BitRegtest, 0x100},
		{"SetReconciliation", ltepwire.BitSetReconciliation, 0x200},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = 0x%x, want 0x%x", c.name, uint64(c.got), uint64(c.want))
		}
	}
}

// buildFacts is the four always-true build-feature RuntimeFacts a Slice-6
// node reports (companion pub/sub, snippet highlight, set-reconciliation).
func buildFacts() ltepwire.RuntimeFacts {
	return ltepwire.RuntimeFacts{
		Reconciliation:   true,
		CompanionPub:     true,
		CompanionSub:     true,
		SnippetHighlight: true,
	}
}

// TestAnnouncedGoldenVectors is the frozen golden table for the SINGLE mask
// producer. Each row pins a fully-explicit (Sharing, RuntimeFacts) input to an
// exact uint64 and its 16-hex rendering. Row 3 is the defect-#3 regression
// guard: the legacy producer OR'd in a static 0x2ED and would have emitted
// 0x2ED here — the rebuild MUST emit 0x2E0.
func TestAnnouncedGoldenVectors(t *testing.T) {
	t.Parallel()
	pub := buildFacts()
	pub.Publishing = true
	reg := buildFacts()
	reg.Regtest = true

	cases := []struct {
		name    string
		sharing ltepwire.Sharing
		facts   ltepwire.RuntimeFacts
		want    uint64
		wantHex string
	}{
		{
			name:    "default_publishing_off",
			sharing: ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true},
			facts:   buildFacts(),
			want:    0x2ED,
			wantHex: "00000000000002ed",
		},
		{
			name:    "default_publishing_on",
			sharing: ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true},
			facts:   pub,
			want:    0x2FD,
			wantHex: "00000000000002fd",
		},
		{
			// Defect #3 regression guard: a full "share nothing" downgrade.
			// Legacy wrongly emitted 0x2ED; the rebuild clears bits 0..3.
			name:    "downgrade_share_nothing",
			sharing: ltepwire.Sharing{ShareLocal: 0, FileHits: false, ContentHits: false},
			facts:   buildFacts(),
			want:    0x2E0,
			wantHex: "00000000000002e0",
		},
		{
			// ShareLocal==1 sets bit 1 ONLY (legacy set bit 0 AND bit 1).
			name:    "swarm_only_file_hits",
			sharing: ltepwire.Sharing{ShareLocal: 1, FileHits: true, ContentHits: false},
			facts:   buildFacts(),
			want:    0x2E6,
			wantHex: "00000000000002e6",
		},
		{
			// Regtest realizes the "loud" announce the legacy never actually set.
			name:    "regtest_loud",
			sharing: ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true},
			facts:   reg,
			want:    0x3ED,
			wantHex: "00000000000003ed",
		},
		{
			// Out-of-range ShareLocal → neither bit 0 nor bit 1.
			name:    "sharelocal_out_of_range",
			sharing: ltepwire.Sharing{ShareLocal: 9, FileHits: true, ContentHits: true},
			facts:   buildFacts(),
			want:    0x2EC, // bits 2,3,5,6,7,9 (no 0, no 1)
			wantHex: "00000000000002ec",
		},
	}
	for _, c := range cases {
		got := ltepwire.Announced(c.sharing, c.facts)
		if got != c.want {
			t.Errorf("%s: Announced = 0x%x, want 0x%x", c.name, got, c.want)
		}
		if hex := ltepwire.FormatHex(got); hex != c.wantHex {
			t.Errorf("%s: FormatHex = %q, want %q", c.name, hex, c.wantHex)
		}
	}
}

// TestUnknownBitsIgnored asserts the forward-compat contract: a reserved/
// future bit riding on a mask is neither honored by Has for a defined bit nor
// causes any rejection — the mask is just data.
func TestUnknownBitsIgnored(t *testing.T) {
	t.Parallel()
	base := ltepwire.Announced(
		ltepwire.Sharing{ShareLocal: 2, FileHits: true, ContentHits: true},
		buildFacts(),
	)
	// OR on some far-future reserved bits (50, 55, 63).
	withFuture := ltepwire.ServiceBits(base) | (1 << 50) | (1 << 55) | (1 << 63)
	if !withFuture.Has(ltepwire.BitShareLocal) {
		t.Error("defined bit lost when unknown bits present")
	}
	if withFuture.Has(ltepwire.BitLayerDPublisher) {
		t.Error("undefined-here bit 4 spuriously reported set")
	}
	// The low 10 bits must be unchanged by the future bits.
	if uint64(withFuture)&0x3FF != base {
		t.Errorf("future bits corrupted the defined range: %#x vs %#x", uint64(withFuture)&0x3FF, base)
	}
}

// TestFormatHexShape checks the rendering contract independently of Announced.
func TestFormatHexShape(t *testing.T) {
	t.Parallel()
	cases := map[uint64]string{
		0x0:                "0000000000000000",
		0x2ED:              "00000000000002ed",
		0x3ED:              "00000000000003ed",
		0xFFFFFFFFFFFFFFFF: "ffffffffffffffff",
	}
	for v, want := range cases {
		if got := ltepwire.FormatHex(v); got != want {
			t.Errorf("FormatHex(%#x) = %q, want %q", v, got, want)
		}
		if len(ltepwire.FormatHex(v)) != 16 {
			t.Errorf("FormatHex(%#x) length = %d, want 16", v, len(ltepwire.FormatHex(v)))
		}
	}
}

// TestWithWithoutHas exercises the bit helpers.
func TestWithWithoutHas(t *testing.T) {
	t.Parallel()
	var s ltepwire.ServiceBits
	s = s.With(ltepwire.BitShareLocal).With(ltepwire.BitRegtest)
	if !s.Has(ltepwire.BitShareLocal) || !s.Has(ltepwire.BitRegtest) {
		t.Fatalf("With did not set bits: %#x", uint64(s))
	}
	s = s.Without(ltepwire.BitRegtest)
	if s.Has(ltepwire.BitRegtest) {
		t.Errorf("Without did not clear bit: %#x", uint64(s))
	}
	if !s.Has(ltepwire.BitShareLocal) {
		t.Errorf("Without cleared the wrong bit: %#x", uint64(s))
	}
}
