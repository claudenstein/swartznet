package sign

import (
	"crypto/ed25519"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Frozen golden vector (ed25519 is deterministic): seed 000102…1e1f signing
// the payload for the contracts/bencode singleFileTorrent fixture's infohash.
const (
	goldenSeedHex     = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	goldenInfoHashHex = "4f1ac1c3f47f60c43b9a63cdc6ba7c893d40faeb"
	goldenPayloadHex  = "534e2d544f5252454e542d56317c4f1ac1c3f47f60c43b9a63cdc6ba7c893d40faeb"
	goldenPubKeyHex   = "03a107bff3ce10be1d70dd18e74bc09967e4d6309ba50d5f1ddc8664125531b8"
	goldenSigHex      = "13533d426d89dbdfaf5476b6ea203f73b78156e7b96f0fbd63fe373436d02af2e835a23b9aeb62f6c630ffac867910b5bc59bab6f25c13c2280aa02541642d0e"
)

func goldenKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(goldenSeedHex)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func goldenInfoHash(t *testing.T) [20]byte {
	t.Helper()
	raw, err := hex.DecodeString(goldenInfoHashHex)
	if err != nil {
		t.Fatal(err)
	}
	var ih [20]byte
	copy(ih[:], raw)
	return ih
}

func TestPayloadGolden(t *testing.T) {
	p := Payload(goldenInfoHash(t))
	if len(p) != 34 {
		t.Fatalf("payload length = %d, want exactly 34", len(p))
	}
	if hex.EncodeToString(p) != goldenPayloadHex {
		t.Fatalf("payload = %x, want %s", p, goldenPayloadHex)
	}
	if !strings.HasPrefix(string(p), "SN-TORRENT-V1|") {
		t.Fatal("payload lacks the byte-exact domain prefix")
	}
}

func TestSignGolden(t *testing.T) {
	priv := goldenKey(t)
	sig, err := Sign(priv, goldenInfoHash(t))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(sig) != goldenSigHex {
		t.Fatalf("sig = %x, want %s", sig, goldenSigHex)
	}
	pub := priv.Public().(ed25519.PublicKey)
	if hex.EncodeToString(pub) != goldenPubKeyHex {
		t.Fatalf("pubkey = %x", pub)
	}
}

func TestSignBadKeyLength(t *testing.T) {
	_, err := Sign(ed25519.PrivateKey(make([]byte, 32)), goldenInfoHash(t))
	if err == nil || !strings.Contains(err.Error(), "bad private key length 32") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyTaxonomy(t *testing.T) {
	priv := goldenKey(t)
	pub := priv.Public().(ed25519.PublicKey)
	ih := goldenInfoHash(t)
	sig, err := Sign(priv, ih)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success populates everything", func(t *testing.T) {
		got, err := Verify(pub, sig, ih)
		if err != nil {
			t.Fatal(err)
		}
		if got.PubKeyHex() != goldenPubKeyHex || got.InfoHash != ih {
			t.Fatalf("signature = %+v", got)
		}
	})

	t.Run("byte-flip is ErrBadSignature with populated Signature", func(t *testing.T) {
		bad := append([]byte(nil), sig...)
		bad[0] ^= 0x01
		got, err := Verify(pub, bad, ih)
		if !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v, want ErrBadSignature", err)
		}
		// The claimed key must still be readable — UIs show it.
		if got.PubKeyHex() != goldenPubKeyHex {
			t.Fatalf("Signature not populated on ErrBadSignature: %+v", got)
		}
	})

	t.Run("wrong infohash is ErrBadSignature", func(t *testing.T) {
		other := sha1.Sum([]byte("different content"))
		if _, err := Verify(pub, sig, other); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("bad lengths are plain errors, neither sentinel", func(t *testing.T) {
		_, err := Verify(pub[:31], sig, ih)
		if err == nil || errors.Is(err, ErrBadSignature) || errors.Is(err, ErrNotSigned) ||
			!strings.Contains(err.Error(), "bad pubkey length 31") {
			t.Fatalf("short pubkey: err = %v", err)
		}
		_, err = Verify(pub, sig[:63], ih)
		if err == nil || errors.Is(err, ErrBadSignature) || errors.Is(err, ErrNotSigned) ||
			!strings.Contains(err.Error(), "bad sig length 63") {
			t.Fatalf("short sig: err = %v", err)
		}
	})
}

func TestSentinelStrings(t *testing.T) {
	if ErrNotSigned.Error() != "signing: torrent is not signed" {
		t.Fatalf("ErrNotSigned = %q", ErrNotSigned)
	}
	if ErrBadSignature.Error() != "signing: signature does not verify" {
		t.Fatalf("ErrBadSignature = %q", ErrBadSignature)
	}
}
