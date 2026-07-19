package record_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"github.com/swartznet/swartznet/contracts/record"
)

func recA() record.Record {
	var pk [32]byte
	pk[0] = 0xAA
	var ih [20]byte
	ih[0] = 0x11
	return record.Record{Pk: pk, Kw: "linux", Ih: ih, T: 1712649600, Pow: 0}
}

// TestElementIDGolden pins the RIBLT reconciliation key + sig message bytes.
func TestElementIDGolden(t *testing.T) {
	t.Parallel()
	a := recA()
	if got := hex.EncodeToString(idBytes(a.ElementID())); got != "64bcf23a1e274246f1f82872d0ee10c0ddd6024dd396370b65d26277cd3f6ba0" {
		t.Errorf("A ElementID = %s", got)
	}
	// sig message = pk(32) || "linux"(5) || ih(20) || LE64(t)(8) || uvarint(0)(1) = 66 bytes
	if got := a.SigMessage(); len(got) != 66 || hex.EncodeToString(got[57:]) != "80f514660000000000" {
		t.Errorf("A sigMsg len=%d tail=%s", len(got), hex.EncodeToString(got[57:]))
	}

	// C: all-zero pk/ih, kw="a", t=0.
	c := record.Record{Kw: "a", T: 0}
	if got := hex.EncodeToString(idBytes(c.ElementID())); got != "193aa0b8735340fedf7f0aa52873c3c5f0b77a67b2b9b2f60c25d3e616d2cf41" {
		t.Errorf("C ElementID = %s", got)
	}
	if len(c.SigMessage()) != 62 {
		t.Errorf("C sigMsg len = %d, want 62", len(c.SigMessage()))
	}
}

// TestElementIDExcludesPowSig is the dedup property: two records identical
// except pow/sig share one ElementID.
func TestElementIDExcludesPowSig(t *testing.T) {
	t.Parallel()
	a := recA()
	b := recA()
	b.Pow = 300
	b.Sig[0] = 0xFF
	if a.ElementID() != b.ElementID() {
		t.Error("ElementID must exclude Pow and Sig (re-signed records must dedupe)")
	}
	// But the sig message DOES include pow (67 bytes for uvarint(300)=ac02).
	if len(b.SigMessage()) != 67 {
		t.Errorf("B sigMsg len = %d, want 67", len(b.SigMessage()))
	}
}

// TestSignVerifyRoundTrip: a mined+signed record verifies; tampering fails.
func TestSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(nil)
	var pk [32]byte
	copy(pk[:], pub)
	var ih [20]byte
	ih[3] = 0x42
	r, err := record.SignAndMine(priv, pk, "ubuntu", ih, 1712649600, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("fresh record must verify: %v", err)
	}
	r.T++ // tamper
	if r.Verify() == nil {
		t.Error("tampered record must not verify")
	}
}

// TestKeywordBounds: empty and >64-byte keywords are rejected at mint.
func TestKeywordBounds(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(nil)
	var pk [32]byte
	if _, err := record.SignAndMine(priv, pk, "", [20]byte{}, 0, 0); err != record.ErrKeyword {
		t.Errorf("empty kw = %v, want ErrKeyword", err)
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := record.SignAndMine(priv, pk, string(long), [20]byte{}, 0, 0); err != record.ErrKeyword {
		t.Errorf("65-byte kw = %v, want ErrKeyword", err)
	}
}

// TestPoWMineVerify: a small PoW target is mineable and verifies.
func TestPoWMineVerify(t *testing.T) {
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(nil)
	var pk [32]byte
	copy(pk[:], pub)
	r, err := record.SignAndMine(priv, pk, "linux", [20]byte{}, 1, 8) // 8 leading zero bits
	if err != nil {
		t.Fatal(err)
	}
	if err := r.VerifyPoW(8); err != nil {
		t.Errorf("mined record should satisfy 8-bit PoW: %v", err)
	}
	if r.VerifyPoW(24) == nil {
		t.Error("8-bit-mined record should not satisfy 24-bit PoW (probabilistically)")
	}
	if r.Verify() != nil {
		t.Error("mined record must still verify its signature")
	}
	if _, err := record.SignAndMine(priv, pk, "x", [20]byte{}, 0, 64); err != record.ErrPoWTarget {
		t.Errorf("bits>40 = %v, want ErrPoWTarget", err)
	}
}

func idBytes(id [32]byte) []byte { return id[:] }
