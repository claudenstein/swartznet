package dhtschema_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// TestPPMISaltGolden pins the frozen salt = SHA256("snet.index").
func TestPPMISaltGolden(t *testing.T) {
	t.Parallel()
	const golden = "89fe1aae185c70dbf9d4ccaa44f9996a3d0d00e55eb2821ad9b143401fbc965d"
	if got := hex.EncodeToString(dhtschema.PPMISalt); got != golden {
		t.Errorf("PPMISalt = %s, want %s", got, golden)
	}
	// Independently re-derive.
	sum := sha256.Sum256([]byte("snet.index"))
	if !bytes.Equal(dhtschema.PPMISalt, sum[:]) {
		t.Error("PPMISalt is not SHA256(\"snet.index\")")
	}
	if len(dhtschema.PPMISalt) != 32 {
		t.Errorf("salt len = %d, want 32", len(dhtschema.PPMISalt))
	}
}

// TestPPMIEncodeGolden pins the minimal item wire bytes.
func TestPPMIEncodeGolden(t *testing.T) {
	t.Parallel()
	v := dhtschema.PPMIValue{IH: make([]byte, 20), Ts: 1700000000}
	out, err := dhtschema.EncodePPMI(v)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "64323a696832303a0000000000000000000000000000000000000000323a747369313730303030303030306565"
	if got := hex.EncodeToString(out); got != golden {
		t.Errorf("minimal PPMI wire drift:\n got %s\nwant %s", got, golden)
	}
	if len(out) != 45 {
		t.Errorf("minimal PPMI = %d bytes, want 45", len(out))
	}
}

func TestPPMIRoundTripWithCommit(t *testing.T) {
	t.Parallel()
	ih := bytes.Repeat([]byte{0x11}, 20)
	commit := bytes.Repeat([]byte{0xab}, 32)
	v := dhtschema.PPMIValue{IH: ih, Commit: commit, Ts: 1700000000}
	out, err := dhtschema.EncodePPMI(v)
	if err != nil {
		t.Fatal(err)
	}
	got, err := dhtschema.DecodePPMI(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.IH, ih) || !bytes.Equal(got.Commit, commit) || got.Ts != 1700000000 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestPPMIEncodeStampsTs(t *testing.T) {
	t.Parallel()
	out, err := dhtschema.EncodePPMI(dhtschema.PPMIValue{IH: make([]byte, 20)})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := dhtschema.DecodePPMI(out)
	if got.Ts == 0 {
		t.Error("Ts not stamped at encode")
	}
}

func TestPPMIFieldWidthValidation(t *testing.T) {
	t.Parallel()
	// Bad IH length.
	if _, err := dhtschema.EncodePPMI(dhtschema.PPMIValue{IH: make([]byte, 10), Ts: 1}); err == nil {
		t.Error("short ih accepted")
	}
	// Bad commit length.
	if _, err := dhtschema.EncodePPMI(dhtschema.PPMIValue{IH: make([]byte, 20), Commit: make([]byte, 10), Ts: 1}); err == nil {
		t.Error("bad commit length accepted")
	}
	// Decode rejects empty + oversize before unmarshal.
	if _, err := dhtschema.DecodePPMI(nil); err == nil {
		t.Error("empty payload accepted")
	}
	if _, err := dhtschema.DecodePPMI(make([]byte, dhtschema.MaxPPMIValueBytes+1)); err == nil {
		t.Error("oversize payload accepted")
	}
}
