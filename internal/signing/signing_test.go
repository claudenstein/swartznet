package signing

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	abencode "github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/contracts/bencode"
	"github.com/swartznet/swartznet/internal/identity"
)

// unsignedFixture mirrors contracts/bencode's frozen single-file vector.
func unsignedFixture() []byte {
	info := "d6:lengthi96e4:name11:fixture.bin12:piece lengthi32768e6:pieces20:aaaaaaaaaaaaaaaaaaaae"
	return []byte("d8:announce20:http://tr.invalid/an4:info" + info + "e")
}

func testSigner(t *testing.T) (identity.Signer, string) {
	t.Helper()
	id, err := identity.Load(filepath.Join(t.TempDir(), "identity.key"), true)
	if err != nil {
		t.Fatal(err)
	}
	return id.Signer(), id.PublicKeyHex()
}

func TestSignVerifyRoundTrip(t *testing.T) {
	signer, pubHex := testSigner(t)
	raw := unsignedFixture()
	signed, err := Sign(raw, signer)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	if sig.PubKeyHex() != pubHex {
		t.Fatalf("verified pubkey %s, want %s", sig.PubKeyHex(), pubHex)
	}
	// Signing adds exactly the two snet fields: 126 bytes.
	if len(signed)-len(raw) != 126 {
		t.Fatalf("signing added %d bytes, want exactly 126", len(signed)-len(raw))
	}
}

// TestTwinInfohashAndInfoBytes is THE Slice-3 property: the info value passes
// through byte-identically, so signed/unsigned twins share one infohash.
func TestTwinInfohashAndInfoBytes(t *testing.T) {
	signer, _ := testSigner(t)
	raw := unsignedFixture()
	signed, err := Sign(raw, signer)
	if err != nil {
		t.Fatal(err)
	}
	mPlain, err := bencode.ParseMetainfo(raw)
	if err != nil {
		t.Fatal(err)
	}
	mSigned, err := bencode.ParseMetainfo(signed)
	if err != nil {
		t.Fatal(err)
	}
	if mPlain.InfoHash != mSigned.InfoHash {
		t.Fatal("signing changed the infohash")
	}
	if !bytes.Equal(mPlain.InfoBytes, mSigned.InfoBytes) {
		t.Fatal("info bytes not byte-identical after signing")
	}
	// Unknown top-level keys survive.
	if !bytes.Contains(signed, []byte("8:announce20:http://tr.invalid/an")) {
		t.Fatal("announce key lost during signing")
	}
}

func TestResignReplacesSignature(t *testing.T) {
	signer1, _ := testSigner(t)
	signer2, pub2 := testSigner(t)
	signed1, err := Sign(unsignedFixture(), signer1)
	if err != nil {
		t.Fatal(err)
	}
	signed2, err := Sign(signed1, signer2)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Verify(signed2)
	if err != nil {
		t.Fatal(err)
	}
	if sig.PubKeyHex() != pub2 {
		t.Fatalf("re-sign did not replace: verified %s, want %s", sig.PubKeyHex(), pub2)
	}
}

func TestVerifyTaxonomy(t *testing.T) {
	signer, _ := testSigner(t)
	signed, err := Sign(unsignedFixture(), signer)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("unsigned is ErrNotSigned", func(t *testing.T) {
		_, err := Verify(unsignedFixture())
		if !errors.Is(err, ErrNotSigned) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("one field without the other is ErrNotSigned", func(t *testing.T) {
		top, err := bencode.DecodeDict(signed)
		if err != nil {
			t.Fatal(err)
		}
		delete(top, "snet.sig")
		partial, err := bencode.EncodeDict(top)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(partial); !errors.Is(err, ErrNotSigned) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("byte-flipped sig is ErrBadSignature with populated Signature", func(t *testing.T) {
		flipped := append([]byte(nil), signed...)
		// Flip a byte inside the signature value (the last 5 bytes are
		// within snet.sig's 64-byte payload before the trailing 'e').
		flipped[len(flipped)-5] ^= 0x01
		sig, err := Verify(flipped)
		if !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v", err)
		}
		if sig.PubKeyHex() == "" || sig.PubKeyHex() == strings.Repeat("0", 64) {
			t.Fatal("Signature not populated on ErrBadSignature")
		}
	})

	t.Run("wrong pubkey length is a plain error", func(t *testing.T) {
		top, err := bencode.DecodeDict(signed)
		if err != nil {
			t.Fatal(err)
		}
		short, _ := abencode.Marshal("tooshort")
		top["snet.pubkey"] = bencode.Bytes(short)
		mangled, err := bencode.EncodeDict(top)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Verify(mangled)
		if err == nil || errors.Is(err, ErrNotSigned) || errors.Is(err, ErrBadSignature) ||
			!strings.Contains(err.Error(), "bad pubkey length 8") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("undecodable pubkey is a plain decode error", func(t *testing.T) {
		top, err := bencode.DecodeDict(signed)
		if err != nil {
			t.Fatal(err)
		}
		intVal, _ := abencode.Marshal(42)
		top["snet.pubkey"] = bencode.Bytes(intVal)
		mangled, err := bencode.EncodeDict(top)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Verify(mangled)
		if err == nil || errors.Is(err, ErrNotSigned) || errors.Is(err, ErrBadSignature) ||
			!strings.Contains(err.Error(), "decode pubkey") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("missing info fires before not-signed", func(t *testing.T) {
		_, err := Verify([]byte("d11:snet.pubkey3:abce"))
		if err == nil || errors.Is(err, ErrNotSigned) ||
			!strings.Contains(err.Error(), "missing info dict") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("garbage is a decode error", func(t *testing.T) {
		_, err := Verify([]byte("not bencode"))
		if err == nil || !strings.Contains(err.Error(), "signing: decode metainfo:") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("singleton-list-wrapped fields verify (lenient, pinned)", func(t *testing.T) {
		// anacrolix Unmarshal unwraps a one-element list into a scalar. The
		// leniency is pinned as accepted surface: a signature wrapped as
		// l32:<pk>e / l64:<sig>e still verifies. Frozen so a future codec
		// change is a deliberate decision, not silent drift.
		top, err := bencode.DecodeDict(signed)
		if err != nil {
			t.Fatal(err)
		}
		top["snet.pubkey"] = bencode.Bytes("l" + string(top["snet.pubkey"]) + "e")
		top["snet.sig"] = bencode.Bytes("l" + string(top["snet.sig"]) + "e")
		wrapped, err := bencode.EncodeDict(top)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(wrapped); err != nil {
			t.Fatalf("singleton-list leniency changed: %v", err)
		}
	})
}
