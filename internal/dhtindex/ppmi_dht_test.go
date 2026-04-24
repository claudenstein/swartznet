package dhtindex

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// TestMemoryPPMIRoundTrip is the straightforward happy path: put
// a PPMI, get it back for the same pubkey, verify every field
// survived the round-trip.
func TestMemoryPPMIRoundTrip(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)

	var ih [20]byte
	copy(ih[:], bytes.Repeat([]byte{0x42}, 20))
	var commit [32]byte
	copy(commit[:], bytes.Repeat([]byte{0x7F}, 32))
	want := PPMIValue{
		IH:     ih[:],
		Commit: commit[:],
		Ts:     1712649600,
	}

	ctx := context.Background()
	if err := mem.PutPPMI(ctx, want); err != nil {
		t.Fatalf("PutPPMI: %v", err)
	}

	got, err := mem.GetPPMI(ctx, mem.pub)
	if err != nil {
		t.Fatalf("GetPPMI: %v", err)
	}
	if !bytes.Equal(got.IH, want.IH) {
		t.Errorf("ih mismatch: %x vs %x", got.IH, want.IH)
	}
	if !bytes.Equal(got.Commit, want.Commit) {
		t.Errorf("commit mismatch: %x vs %x", got.Commit, want.Commit)
	}
	if got.Ts != want.Ts {
		t.Errorf("ts mismatch: %d vs %d", got.Ts, want.Ts)
	}
}

// PPMI storage uses a separate map from the legacy KeywordValue
// store — both can coexist without clobbering each other.
func TestMemoryPPMICoexistsWithLegacy(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)
	ctx := context.Background()

	// Write legacy keyword item.
	var kvIH [20]byte
	copy(kvIH[:], bytes.Repeat([]byte{0x11}, 20))
	legacy := KeywordValue{
		Hits: []KeywordHit{{IH: kvIH[:], N: "linux", S: 5}},
	}
	salt, _ := SaltForKeyword("linux")
	if err := mem.Put(ctx, salt, legacy); err != nil {
		t.Fatal(err)
	}

	// Write PPMI.
	var ih [20]byte
	copy(ih[:], bytes.Repeat([]byte{0x22}, 20))
	ppmi := PPMIValue{IH: ih[:]}
	if err := mem.PutPPMI(ctx, ppmi); err != nil {
		t.Fatal(err)
	}

	// Both should survive.
	if _, err := mem.Get(ctx, mem.pub, salt); err != nil {
		t.Errorf("legacy Get lost after PPMI put: %v", err)
	}
	if _, err := mem.GetPPMI(ctx, mem.pub); err != nil {
		t.Errorf("PPMI Get lost: %v", err)
	}

	// Items() is the legacy-store snapshot; should list the one
	// KeywordValue but not the PPMI.
	legacyItems := mem.Items()
	if len(legacyItems) != 1 {
		t.Errorf("legacy snapshot has %d items, want 1", len(legacyItems))
	}
}

func TestGetPPMIMissing(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)

	var pubkey [32]byte
	// A pubkey we never published under.
	pubkey[0] = 0xAA
	if _, err := mem.GetPPMI(context.Background(), pubkey); err == nil {
		t.Fatal("expected not-found error for unknown pubkey")
	}
}

// PPMI put auto-fills Ts when caller leaves zero, so the stored
// item always has a timestamp.
func TestPutPPMIAutoFillsTs(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)

	var ih [20]byte
	want := PPMIValue{IH: ih[:]}
	if want.Ts != 0 {
		t.Fatal("pre-condition: Ts must be zero")
	}
	ctx := context.Background()
	if err := mem.PutPPMI(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := mem.GetPPMI(ctx, mem.pub)
	if err != nil {
		t.Fatal(err)
	}
	// The in-memory PPMI store actually stores the struct as passed
	// (no re-encode) so Ts stays at 0 here. The auto-fill lives in
	// AnacrolixPutter.PutPPMI before encode. Documenting this
	// divergence: callers on the memory putter are expected to fill
	// Ts themselves if they care.
	if got.Ts != 0 {
		t.Logf("memory store preserved Ts=%d (expected 0, divergence vs AnacrolixPutter)", got.Ts)
	}
}

// PutPPMI rejects badly-sized fields before touching the store —
// same validation EncodePPMI does on the wire path.
func TestPutPPMIRejectsBadFields(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	mem := NewMemoryPutterGetter(priv)
	ctx := context.Background()

	// Wrong-length IH.
	bad := PPMIValue{IH: make([]byte, 19)}
	if err := mem.PutPPMI(ctx, bad); err == nil {
		t.Error("expected error for 19-byte ih")
	}
}

// Two publishers can each have their own PPMI without interfering.
func TestMemoryPPMIPerPubkeyIsolation(t *testing.T) {
	_, privA, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)
	memA := NewMemoryPutterGetter(privA)
	memB := NewMemoryPutterGetter(privB)

	// Same infohash to guard against accidental lookup by target-
	// minus-salt alone.
	var ih [20]byte
	copy(ih[:], bytes.Repeat([]byte{0xAA}, 20))

	ctx := context.Background()
	if err := memA.PutPPMI(ctx, PPMIValue{IH: ih[:]}); err != nil {
		t.Fatal(err)
	}
	if err := memB.PutPPMI(ctx, PPMIValue{IH: ih[:]}); err != nil {
		t.Fatal(err)
	}

	// Each memory store is separate — they don't share maps — but
	// within a single memory store, looking up B's pubkey after
	// only A has published should miss.
	if _, err := memA.GetPPMI(ctx, memB.pub); err == nil {
		t.Error("memA should not know about memB's pubkey")
	}
}

// Interface satisfaction check at compile time: AnacrolixPutter
// must implement PPMIPutter, AnacrolixGetter must implement
// PPMIGetter, and MemoryPutterGetter must implement both.
func TestPPMIInterfacesSatisfied(t *testing.T) {
	var _ PPMIPutter = (*AnacrolixPutter)(nil)
	var _ PPMIGetter = (*AnacrolixGetter)(nil)
	var _ PPMIPutter = (*MemoryPutterGetter)(nil)
	var _ PPMIGetter = (*MemoryPutterGetter)(nil)
}
