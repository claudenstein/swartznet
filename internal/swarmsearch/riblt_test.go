package swarmsearch

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"testing"
)

// elementFromString generates a deterministic RIBLTElement for a
// label. Used in tests to build reproducible sets.
func elementFromString(s string) RIBLTElement {
	return RIBLTElement(sha256.Sum256([]byte(s)))
}

// syncSets runs a full encoder/decoder exchange and returns the
// number of symbols consumed. Fails the test if convergence
// takes more than maxSymbols. Uses a stable-rounds heuristic: the
// decoder can't know the target diff count a priori, so we require
// both (a) all residual symbols zeroed and (b) no new decodings
// for `stableThreshold` consecutive symbols before declaring
// convergence. This is the same shape as the sync_end flow in
// SPEC §2.2 where each side sends sync_need when its decoder
// stops making progress.
func syncSets(t *testing.T, sender, receiver []RIBLTElement, maxSymbols int) ([]RIBLTElement, []RIBLTElement, int) {
	t.Helper()
	const stableThreshold = 20
	enc := NewRIBLTEncoder()
	for _, e := range sender {
		enc.AddElement(e)
	}
	dec := NewRIBLTDecoder()
	for _, e := range receiver {
		dec.AddLocalElement(e)
	}
	stable := 0
	lastDecoded := 0
	for n := 0; n < maxSymbols; n++ {
		dec.AddRemoteSymbol(enc.NextSymbol())
		if len(dec.decoded) == lastDecoded {
			stable++
		} else {
			stable = 0
			lastDecoded = len(dec.decoded)
		}
		if stable >= stableThreshold && dec.Converged() {
			return dec.Added(), dec.Removed(), n + 1
		}
	}
	t.Fatalf("did not converge after %d symbols (added=%d removed=%d)",
		maxSymbols, len(dec.Added()), len(dec.Removed()))
	return nil, nil, maxSymbols
}

func TestConverge_Diff0(t *testing.T) {
	set := []RIBLTElement{
		elementFromString("a"),
		elementFromString("b"),
		elementFromString("c"),
	}
	added, removed, n := syncSets(t, set, set, 50)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("identical sets should yield no differences, got +%d -%d",
			len(added), len(removed))
	}
	t.Logf("diff=0 converged in %d symbols", n)
}

func TestConverge_Diff1_SenderHas(t *testing.T) {
	sender := []RIBLTElement{
		elementFromString("a"),
		elementFromString("b"),
		elementFromString("c"),
	}
	receiver := []RIBLTElement{
		elementFromString("a"),
		elementFromString("b"),
	}
	added, removed, n := syncSets(t, sender, receiver, 50)
	if len(added) != 1 {
		t.Fatalf("added = %v, want 1 (sender has c)", added)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want 0", removed)
	}
	want := elementFromString("c")
	if added[0] != want {
		t.Errorf("added[0] != expected c")
	}
	t.Logf("diff=1 converged in %d symbols", n)
}

func TestConverge_Diff1_ReceiverHas(t *testing.T) {
	sender := []RIBLTElement{
		elementFromString("a"),
		elementFromString("b"),
	}
	receiver := []RIBLTElement{
		elementFromString("a"),
		elementFromString("b"),
		elementFromString("x"),
	}
	added, removed, n := syncSets(t, sender, receiver, 50)
	if len(added) != 0 {
		t.Errorf("added = %v, want 0", added)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v, want 1 (receiver has x)", removed)
	}
	if removed[0] != elementFromString("x") {
		t.Errorf("removed[0] != expected x")
	}
	t.Logf("diff=1 reversed converged in %d symbols", n)
}

func TestConverge_Diff_Symmetric(t *testing.T) {
	// 10 shared + 5 sender-only + 3 receiver-only = 8 differences
	var sender, receiver []RIBLTElement
	for i := 0; i < 10; i++ {
		e := elementFromString(fmt.Sprintf("shared-%d", i))
		sender = append(sender, e)
		receiver = append(receiver, e)
	}
	for i := 0; i < 5; i++ {
		sender = append(sender, elementFromString(fmt.Sprintf("snd-%d", i)))
	}
	for i := 0; i < 3; i++ {
		receiver = append(receiver, elementFromString(fmt.Sprintf("rcv-%d", i)))
	}
	added, removed, n := syncSets(t, sender, receiver, 200)
	if len(added) != 5 {
		t.Errorf("added = %d, want 5", len(added))
	}
	if len(removed) != 3 {
		t.Errorf("removed = %d, want 3", len(removed))
	}
	t.Logf("diff=8 symmetric converged in %d symbols", n)
}

func TestConverge_Diff100(t *testing.T) {
	// 500 shared + 100 differences total (50 each side)
	rng := rand.New(rand.NewSource(1))
	_ = rng
	var sender, receiver []RIBLTElement
	for i := 0; i < 500; i++ {
		e := elementFromString(fmt.Sprintf("shared100-%d", i))
		sender = append(sender, e)
		receiver = append(receiver, e)
	}
	for i := 0; i < 50; i++ {
		sender = append(sender, elementFromString(fmt.Sprintf("s100-%d", i)))
		receiver = append(receiver, elementFromString(fmt.Sprintf("r100-%d", i)))
	}
	added, removed, n := syncSets(t, sender, receiver, 2000)
	if len(added) != 50 || len(removed) != 50 {
		t.Errorf("added=%d removed=%d, want 50 each", len(added), len(removed))
	}
	// Expect O(d) symbols; d=100 → < 1000 realistic budget.
	if n > 1000 {
		t.Errorf("used %d symbols for d=100, expected <1000", n)
	}
	t.Logf("diff=100 converged in %d symbols", n)
}

// Sender unilaterally adds 500 elements. This exercises the
// decoder when diffSymbols grow larger than their "baseline"
// size, which can stress peeling.
func TestConverge_Diff500_OneSide(t *testing.T) {
	var sender, receiver []RIBLTElement
	for i := 0; i < 200; i++ {
		e := elementFromString(fmt.Sprintf("one-side-%d", i))
		sender = append(sender, e)
		receiver = append(receiver, e)
	}
	for i := 0; i < 500; i++ {
		sender = append(sender, elementFromString(fmt.Sprintf("only-s-%d", i)))
	}
	added, removed, n := syncSets(t, sender, receiver, 5000)
	if len(added) != 500 || len(removed) != 0 {
		t.Errorf("added=%d removed=%d, want 500/0", len(added), len(removed))
	}
	t.Logf("diff=500 one-sided converged in %d symbols", n)
}

func TestSymbolBudgetExceededSentinel(t *testing.T) {
	// If decoding fails to converge within the caller's budget
	// they should not mistake an unconverged stream for success.
	var sender []RIBLTElement
	for i := 0; i < 1000; i++ {
		sender = append(sender, elementFromString(fmt.Sprintf("hopeless-%d", i)))
	}
	enc := NewRIBLTEncoder()
	for _, e := range sender {
		enc.AddElement(e)
	}
	dec := NewRIBLTDecoder()

	// Only consume 10 symbols — far below what convergence needs.
	for i := 0; i < 10; i++ {
		dec.AddRemoteSymbol(enc.NextSymbol())
	}
	if dec.Converged() {
		t.Fatal("decoder should not report convergence after 10 symbols with d=1000")
	}
}

// Two encoders with the same input set MUST produce identical
// symbol streams — determinism is load-bearing for the wire
// format, otherwise peers can't both compute the same symbol i.
func TestEncoderDeterminism(t *testing.T) {
	set := []RIBLTElement{
		elementFromString("one"),
		elementFromString("two"),
		elementFromString("three"),
		elementFromString("four"),
	}
	a := NewRIBLTEncoder()
	b := NewRIBLTEncoder()
	for _, e := range set {
		a.AddElement(e)
		b.AddElement(e)
	}
	for i := 0; i < 50; i++ {
		if a.NextSymbol() != b.NextSymbol() {
			t.Fatalf("encoders diverged at symbol %d", i)
		}
	}
}

// contributes() must be a pure function — same input, same output,
// across calls. The SplitMix mixing is deterministic but a dumb
// regression would be catastrophic.
func TestContributesDeterministic(t *testing.T) {
	for i := uint64(0); i < 100; i++ {
		got1 := contributes(0x1234_5678_DEAD_BEEF, i)
		got2 := contributes(0x1234_5678_DEAD_BEEF, i)
		if got1 != got2 {
			t.Fatalf("contributes(key, %d) not deterministic", i)
		}
	}
}

// Over a large sample, contributes() should produce a rate close
// to the theoretical cycle-average (~1/12 × Σ 1/2^k for k=1..12 ≈
// 0.083). The graduated-cycle design means the rate is lower than
// a constant-rate scheme; what matters is that some positions
// generate low-degree symbols for peeling.
func TestContributesRate(t *testing.T) {
	const n = 12000 // multiple of cycle length (12) for tight measurement
	hits := 0
	for i := uint64(0); i < n; i++ {
		if contributes(i*0xA1B2C3D4, i) {
			hits++
		}
	}
	rate := float64(hits) / float64(n)
	// Theoretical ≈ 0.0832; tolerate ±0.02.
	if rate < 0.06 || rate > 0.11 {
		t.Errorf("contribution rate %.3f, want ≈ 0.083", rate)
	}
}

// Length() reports the element count.
func TestEncoderLen(t *testing.T) {
	enc := NewRIBLTEncoder()
	enc.AddElement(elementFromString("x"))
	enc.AddElement(elementFromString("y"))
	if enc.Len() != 2 {
		t.Errorf("Len = %d, want 2", enc.Len())
	}
}

// Symbol struct exports the fields subscriber.go will see.
func TestSymbolFieldsExported(t *testing.T) {
	var s RIBLTSymbol
	s.Count = 1
	s.KeyXOR = 42
	s.DataXOR[0] = 0xAB
	if s.Count != 1 || s.KeyXOR != 42 || s.DataXOR[0] != 0xAB {
		t.Fatal("symbol fields not addressable as expected")
	}
}
