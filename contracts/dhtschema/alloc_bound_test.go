package dhtschema

import (
	"runtime"
	"testing"
)

// TestDecodeValueBoundsAllocation is the regression for the allocation
// amplification: a tiny untrusted value declaring a ~128 MiB inner string must
// be rejected without allocating anywhere near that much (anacrolix bencode's
// default MaxStrLen would make([]byte, declaredLen) before the read fails).
func TestDecodeValueBoundsAllocation(t *testing.T) {
	// 21 bytes declaring a ~128 MiB next_pk string.
	payload := []byte("d7:next_pk134217727:e")
	if alloc, err := allocOf(func() { _, _ = DecodeValue(payload) }); err {
		t.Fatal("unreachable")
	} else if alloc > 8<<20 { // 8 MiB — vastly under the ~128 MiB unbounded case
		t.Errorf("DecodeValue allocated %d bytes for a 21-byte hostile value (unbounded)", alloc)
	}
}

// TestDecodePPMIBoundsAllocation covers the same amplification on the PPMI path.
func TestDecodePPMIBoundsAllocation(t *testing.T) {
	payload := []byte("d2:ih134217727:e") // 16 bytes declaring a ~128 MiB ih string
	if alloc, _ := allocOf(func() { _, _ = DecodePPMI(payload) }); alloc > 8<<20 {
		t.Errorf("DecodePPMI allocated %d bytes for a 16-byte hostile value (unbounded)", alloc)
	}
}

// TestDecodeValueRejectsOversizeString confirms the bound rejects (not accepts)
// an oversize declared string.
func TestDecodeValueRejectsOversizeString(t *testing.T) {
	if _, err := DecodeValue([]byte("d7:next_pk134217727:e")); err == nil {
		t.Error("a 128 MiB-declaring value was accepted")
	}
}

// allocOf returns the bytes allocated while fn runs (best-effort via TotalAlloc,
// which is cumulative and never decreases). The bool return is always false —
// only here so the caller can branch without a separate error.
func allocOf(fn func()) (uint64, bool) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, false
}
