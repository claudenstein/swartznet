package swarmsearch

import "testing"

// TestRIBLTConvergedRejectsNonZeroDataXOR covers Converged's
// `for _, b := range s.DataXOR { if b != 0 { return false } }`
// arm. Crafts a degenerate decoder state: a single diffSymbol
// with Count==0 and KeyXOR==0 but a non-zero DataXOR byte.
// This shape can't arise from healthy peel/encode flow but
// the guard exists as defence-in-depth.
func TestRIBLTConvergedRejectsNonZeroDataXOR(t *testing.T) {
	t.Parallel()
	dec := NewRIBLTDecoder()
	dec.diffSymbols = []RIBLTSymbol{
		{Count: 0, KeyXOR: 0, DataXOR: [32]byte{0x01}},
	}
	if dec.Converged() {
		t.Error("Converged should be false when DataXOR has non-zero bytes")
	}
}
