package riblt_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/swartznet/swartznet/contracts/riblt"
)

func id(label string) riblt.RIBLTElement { return sha256.Sum256([]byte(label)) }

// TestKeyFNVGolden pins the FNV-1a-64 element key over the full 32 bytes.
func TestKeyFNVGolden(t *testing.T) {
	t.Parallel()
	cases := map[string]uint64{
		"a": 0x28E80605A79C8642,
		"b": 0x01BB5C8E53136742,
		"c": 0xA27ABAE3AB0736C8,
	}
	for label, want := range cases {
		if got := id(label).Key(); got != want {
			t.Errorf("Key(sha256(%q)) = %#x, want %#x", label, got, want)
		}
	}
}

// TestContributesSplitMix64Pin pins the membership cycle against a fixed key.
func TestContributesSplitMix64Pin(t *testing.T) {
	t.Parallel()
	const key = 0x12345678DEADBEEF
	want := map[uint64]bool{1: true, 2: true, 12: true}
	for idx := uint64(0); idx < 16; idx++ {
		if got := riblt.Contributes(key, idx); got != want[idx] {
			t.Errorf("Contributes(key, %d) = %v, want %v", idx, got, want[idx])
		}
	}
}

// TestEncodeStreamGolden pins the coded-symbol stream over {a,b,c}.
func TestEncodeStreamGolden(t *testing.T) {
	t.Parallel()
	var enc riblt.Encoder
	enc.AddElement(id("a"))
	enc.AddElement(id("b"))
	enc.AddElement(id("c"))
	if enc.Len() != 3 {
		t.Fatalf("Len = %d", enc.Len())
	}

	abc := mustHex(t, "dac9450763729e62aca78b63cbaae8dc1fa837fa018c71aceb81fc8ad828a7e0")
	type want struct {
		count  int32
		keyXOR uint64
		data   riblt.RIBLTElement
	}
	wants := map[uint64]want{
		0:  {1, 0xA27ABAE3AB0736C8, id("c")},
		1:  {1, 0xA27ABAE3AB0736C8, id("c")},
		12: {3, 0x8B29E0685F88D7C8, abc},
		14: {1, 0x01BB5C8E53136742, id("b")},
	}
	var zero riblt.RIBLTElement
	for idx := uint64(0); idx < 16; idx++ {
		if enc.NextSymbolIndex() != idx {
			t.Fatalf("NextSymbolIndex = %d, want %d", enc.NextSymbolIndex(), idx)
		}
		s := enc.NextSymbol()
		if w, ok := wants[idx]; ok {
			if s.Count != w.count || s.KeyXOR != w.keyXOR || riblt.RIBLTElement(s.DataXOR) != w.data {
				t.Errorf("symbol[%d] = {%d, %#x, %x}, want {%d, %#x, %x}", idx, s.Count, s.KeyXOR, s.DataXOR, w.count, w.keyXOR, w.data)
			}
		} else if s.Count != 0 || s.KeyXOR != 0 || riblt.RIBLTElement(s.DataXOR) != zero {
			t.Errorf("symbol[%d] should be zero, got {%d, %#x, %x}", idx, s.Count, s.KeyXOR, s.DataXOR)
		}
	}
}

// drain applies symbols from enc to dec until converged, requiring a minimum
// number first — rateless-code convergence is a protocol-level batch decision
// ("all residual symbols zero"), which is trivially true on a too-short prefix.
func drain(t *testing.T, enc *riblt.Encoder, dec *riblt.Decoder, minCheck, max int) {
	t.Helper()
	for i := 0; i < max; i++ {
		dec.AddRemoteSymbol(enc.NextSymbol())
		if i+1 >= minCheck && dec.Converged() {
			return
		}
	}
	if !dec.Converged() {
		t.Fatalf("decoder did not converge within %d symbols", max)
	}
}

// TestDecodeSymmetricDiff: sender {a,b,c} vs receiver {a,b} → c is Added.
func TestDecodeSymmetricDiff(t *testing.T) {
	t.Parallel()
	var enc riblt.Encoder
	for _, l := range []string{"a", "b", "c"} {
		enc.AddElement(id(l))
	}
	dec := riblt.NewDecoder()
	dec.AddLocalElement(id("a"))
	dec.AddLocalElement(id("b"))
	drain(t, &enc, dec, 256, 2000)
	added := dec.Added()
	if len(added) != 1 || added[0] != id("c") {
		t.Fatalf("Added = %x, want [c]", added)
	}
	if len(dec.Removed()) != 0 {
		t.Errorf("Removed = %x, want none", dec.Removed())
	}
}

// TestDecodeBidirectional: each side has one element the other lacks.
func TestDecodeBidirectional(t *testing.T) {
	t.Parallel()
	// sender has {a,b,c}, receiver has {a,b,x}. Diff: c Added, x Removed.
	var enc riblt.Encoder
	for _, l := range []string{"a", "b", "c"} {
		enc.AddElement(id(l))
	}
	dec := riblt.NewDecoder()
	for _, l := range []string{"a", "b", "x"} {
		dec.AddLocalElement(id(l))
	}
	drain(t, &enc, dec, 256, 2000)
	if len(dec.Added()) != 1 || dec.Added()[0] != id("c") {
		t.Errorf("Added = %x, want [c]", dec.Added())
	}
	if len(dec.Removed()) != 1 || dec.Removed()[0] != id("x") {
		t.Errorf("Removed = %x, want [x]", dec.Removed())
	}
}

// TestConvergeManyElements: a larger symmetric difference converges multi-batch.
func TestConvergeManyElements(t *testing.T) {
	t.Parallel()
	var enc riblt.Encoder
	dec := riblt.NewDecoder()
	// Shared 200, sender-only 25, receiver-only 25 (50-element diff).
	for i := 0; i < 200; i++ {
		e := id(string(rune('A')) + itoa(i))
		enc.AddElement(e)
		dec.AddLocalElement(e)
	}
	senderOnly := map[riblt.RIBLTElement]bool{}
	for i := 0; i < 25; i++ {
		e := id("S" + itoa(i))
		enc.AddElement(e)
		senderOnly[e] = true
	}
	recvOnly := map[riblt.RIBLTElement]bool{}
	for i := 0; i < 25; i++ {
		e := id("R" + itoa(i))
		dec.AddLocalElement(e)
		recvOnly[e] = true
	}
	drain(t, &enc, dec, 256, 8000)
	if len(dec.Added()) != 25 || len(dec.Removed()) != 25 {
		t.Fatalf("diff sizes: added=%d removed=%d, want 25/25", len(dec.Added()), len(dec.Removed()))
	}
	for _, e := range dec.Added() {
		if !senderOnly[e] {
			t.Errorf("Added contains a non-sender-only element")
		}
	}
	for _, e := range dec.Removed() {
		if !recvOnly[e] {
			t.Errorf("Removed contains a non-receiver-only element")
		}
	}
}

func mustHex(t *testing.T, s string) riblt.RIBLTElement {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad hex %q", s)
	}
	var e riblt.RIBLTElement
	copy(e[:], b)
	return e
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
