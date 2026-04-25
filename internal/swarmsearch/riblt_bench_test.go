package swarmsearch

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// SPEC §7 regression gate: RIBLT convergence bandwidth.
//
// The spec names a 10k-peer, 5M-record scenario targeting <5 kB
// per peer-pair meeting at steady state. We simulate this at the
// unit level by measuring how many symbols it takes to converge
// for a symmetric difference of `d` elements over a 1000-element
// common base.
//
// Symbol size is fixed (~48 bytes bencoded), so symbols-per-sync
// is a direct proxy for bytes-per-sync. The "<5 kB" target
// translates to roughly 5000 / 48 ≈ 100 symbols per sync for
// steady-state diff sizes of d ~30-50.

// Deterministic element factory.
func benchElementForLabel(s string) RIBLTElement {
	return RIBLTElement(sha256.Sum256([]byte(s)))
}

// benchRIBLTConverge drives an encoder/decoder pair through a
// synthetic symmetric-difference scenario.
func benchRIBLTConverge(b *testing.B, common, diff int) {
	senderBase := make([]RIBLTElement, common+diff)
	receiverBase := make([]RIBLTElement, common+diff)
	for i := 0; i < common; i++ {
		e := benchElementForLabel(fmt.Sprintf("shared-%d", i))
		senderBase[i] = e
		receiverBase[i] = e
	}
	for i := 0; i < diff; i++ {
		senderBase[common+i] = benchElementForLabel(fmt.Sprintf("snd-%d", i))
		receiverBase[common+i] = benchElementForLabel(fmt.Sprintf("rcv-%d", i))
	}

	b.ResetTimer()
	b.ReportAllocs()

	const stableThreshold = 20
	maxSymbols := 5000
	totalSymbols := 0
	for n := 0; n < b.N; n++ {
		enc := NewRIBLTEncoder()
		for _, e := range senderBase {
			enc.AddElement(e)
		}
		dec := NewRIBLTDecoder()
		for _, e := range receiverBase {
			dec.AddLocalElement(e)
		}
		stable := 0
		lastDecoded := 0
		converged := false
		for i := 0; i < maxSymbols; i++ {
			dec.AddRemoteSymbol(enc.NextSymbol())
			if len(dec.decoded) == lastDecoded {
				stable++
			} else {
				stable = 0
				lastDecoded = len(dec.decoded)
			}
			if stable >= stableThreshold && dec.Converged() {
				totalSymbols += i + 1
				converged = true
				break
			}
		}
		if !converged {
			b.Fatalf("did not converge at diff=%d", diff)
		}
	}
	if b.N > 0 {
		b.ReportMetric(float64(totalSymbols)/float64(b.N), "symbols/op")
		// A symbol is ~48 bencoded bytes; report bytes/op too so
		// the CI gate can be expressed in bandwidth terms.
		b.ReportMetric(48*float64(totalSymbols)/float64(b.N), "bytes/op")
	}
}

// Diff sizes chosen to bracket the steady-state target: a 10k
// network producing ~1000 new records/hr with 50 peer meetings/hr
// means each peer sees ~50 fresh records per meeting — within
// 5x-10x of Diff10/Diff100.
func BenchmarkRIBLTConverge_Diff0(b *testing.B)   { benchRIBLTConverge(b, 1000, 0) }
func BenchmarkRIBLTConverge_Diff10(b *testing.B)  { benchRIBLTConverge(b, 1000, 10) }
func BenchmarkRIBLTConverge_Diff100(b *testing.B) { benchRIBLTConverge(b, 1000, 100) }
func BenchmarkRIBLTConverge_Diff500(b *testing.B) { benchRIBLTConverge(b, 500, 500) }

// BenchmarkRIBLTEncoderNextSymbol isolates the encode side — raw
// symbol-production cost, independent of decode. Useful for
// measuring regressions in the contributes() hash or the
// XOR accumulation.
func BenchmarkRIBLTEncoderNextSymbol(b *testing.B) {
	enc := NewRIBLTEncoder()
	for i := 0; i < 1000; i++ {
		enc.AddElement(benchElementForLabel(fmt.Sprintf("el-%d", i)))
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = enc.NextSymbol()
	}
}
