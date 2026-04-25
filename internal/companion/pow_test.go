package companion

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
)

// Mining a small difficulty converges fast and verifies cleanly.
// We use bits=8 (256 iterations average) so the test is quick
// under -race.
func TestMineRecordPoWSmallBits(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	var ih [20]byte
	ih[0] = 0xAB
	r := Record{Pk: pk, Kw: "test", Ih: ih, T: 1}

	got, err := MineRecordPoW(r, 8, 0)
	if err != nil {
		t.Fatalf("MineRecordPoW: %v", err)
	}

	// Verify: re-hashing the mined record's sig message should
	// produce a digest with ≥8 leading zero bits.
	sum := sha256.Sum256(RecordSigMessage(got))
	if leadingZeroBitsOfByteSlice(sum[:]) < 8 {
		t.Fatalf("mined record has %d leading zeros, want ≥8",
			leadingZeroBitsOfByteSlice(sum[:]))
	}

	// And VerifyRecordPoW must agree.
	if err := VerifyRecordPoW(got, 8); err != nil {
		t.Fatalf("VerifyRecordPoW rejects mined record: %v", err)
	}
}

func TestMineRecordPoWZeroBitsIsNoOp(t *testing.T) {
	var r Record
	r.Kw = "x"
	got, err := MineRecordPoW(r, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pow != 0 {
		t.Errorf("zero-bits should not touch Pow, got %d", got.Pow)
	}
}

func TestMineRecordPoWRejectsHighBits(t *testing.T) {
	var r Record
	r.Kw = "x"
	if _, err := MineRecordPoW(r, 50, 0); err == nil {
		t.Fatal("expected refusal for prohibitively high bit target")
	}
}

func TestMineRecordPoWExhausts(t *testing.T) {
	var r Record
	r.Kw = "x"
	_, err := MineRecordPoW(r, 24, 16) // absurdly small budget for D=24
	if !errors.Is(err, ErrPoWExhausted) {
		t.Fatalf("want ErrPoWExhausted, got %v", err)
	}
}

func TestSignAndMineRecordRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	ih[0] = 0x42

	r, err := SignAndMineRecord(priv, pub, "ubuntu", ih, 1712649600, 8)
	if err != nil {
		t.Fatalf("SignAndMineRecord: %v", err)
	}

	if err := VerifyRecordSig(r); err != nil {
		t.Fatalf("signature invalid: %v", err)
	}
	if err := VerifyRecordPoW(r, 8); err != nil {
		t.Fatalf("PoW invalid: %v", err)
	}
	if r.Pow == 0 {
		// Extremely unlikely to mine at nonce 0 for D=8; allowed
		// but flag as unusual in case someone broke mining.
		t.Log("note: mined Pow=0, which is valid but rare at D=8")
	}
}

func TestSignAndMineRecordRejectsBadPubLen(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	shortPub := make(ed25519.PublicKey, 16)
	if _, err := SignAndMineRecord(priv, shortPub, "x", ih, 0, 8); err == nil {
		t.Fatal("expected error for wrong-length pubkey")
	}
}

// TestSignAndMineRecordPropagatesPoWError covers the
// `if err != nil` arm after MineRecordPoW. Pass bits=41 so
// MineRecordPoW's "cost prohibitive" guard fires; the wrapped
// error must surface from SignAndMineRecord without producing
// a signed record.
func TestSignAndMineRecordPropagatesPoWError(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	r, err := SignAndMineRecord(priv, pub, "x", ih, 0, 41)
	if err == nil {
		t.Fatal("expected error for prohibitive bits=41")
	}
	// The returned record is the partial mined value; its Sig
	// must remain zero since signing was skipped.
	var zeroSig [64]byte
	if r.Sig != zeroSig {
		t.Error("Sig should be zero on PoW failure (signing skipped)")
	}
}

// Sanity: recordPreimage and RecordSigMessage MUST produce
// identical bytes — otherwise a miner would solve for one preimage
// and a verifier would check a different one.
func TestPreimageMatchesSigMessage(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	r := mkRecord(t, pub, priv, "test", ih, 100, 42)
	if !bytes.Equal(recordPreimage(r), RecordSigMessage(r)) {
		t.Fatal("recordPreimage vs RecordSigMessage differ — miner and verifier will disagree")
	}
}

// A mined + signed record that is later tampered with must fail
// both the PoW check AND the signature check.
func TestSignAndMineDetectsTamper(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var ih [20]byte
	r, err := SignAndMineRecord(priv, pub, "tampered", ih, 5, 8)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate T after signing. PoW no longer holds (different
	// preimage), signature no longer valid (different signed bytes).
	r.T = 9999
	if err := VerifyRecordPoW(r, 8); err == nil {
		t.Error("expected tampered T to break PoW")
	}
	if err := VerifyRecordSig(r); err == nil {
		t.Error("expected tampered T to break signature")
	}
}

// Mining the same record from the same minNonce base is deterministic.
// Mining at different difficulties produces different nonces.
func TestMineDeterministic(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	var ih [20]byte
	r := Record{Pk: pk, Kw: "det", Ih: ih, T: 1}

	a, err := MineRecordPoW(r, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := MineRecordPoW(r, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.Pow != b.Pow {
		t.Errorf("mining not deterministic: %d vs %d", a.Pow, b.Pow)
	}
}
