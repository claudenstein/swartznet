package signing_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/signing"
)

// miniTorrent builds a tiny but valid bencoded .torrent-like
// dict so we can test signing without dragging in the whole
// engine. The info dict here is minimal; signing only cares about
// the bencoded bytes, not their semantic correctness.
func miniTorrent(t *testing.T) []byte {
	t.Helper()
	mi := map[string]interface{}{
		"announce": "http://tracker.example.com/announce",
		"info": map[string]interface{}{
			"name":         "test",
			"piece length": 16384,
			"pieces":       string(make([]byte, 20)),
			"length":       4,
		},
		"created by": "SwartzNet test",
	}
	out, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("marshal mini torrent: %v", err)
	}
	return out
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

// TestSignVerifyRoundTrip signs a torrent and verifies the result.
func TestSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	pub, priv := newKey(t)
	raw := miniTorrent(t)

	signed, err := signing.SignBytes(raw, priv)
	if err != nil {
		t.Fatalf("SignBytes: %v", err)
	}
	if len(signed) <= len(raw) {
		t.Errorf("signed bytes should be longer than raw: %d vs %d", len(signed), len(raw))
	}

	sig, err := signing.VerifyBytes(signed)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}

	if string(sig.PubKey[:]) != string(pub) {
		t.Errorf("pubkey mismatch")
	}
	if sig.InfoHash == [20]byte{} {
		t.Error("infohash is zero")
	}
}

// TestVerifyUnsignedReturnsErrNotSigned covers the "most torrents
// in the world are not signed" case.
func TestVerifyUnsignedReturnsErrNotSigned(t *testing.T) {
	t.Parallel()

	raw := miniTorrent(t)
	_, err := signing.VerifyBytes(raw)
	if !errors.Is(err, signing.ErrNotSigned) {
		t.Errorf("got %v, want ErrNotSigned", err)
	}
}

// TestVerifyTamperedInfoFailsSignature signs a torrent, then
// modifies the info dict and verifies that the signature no
// longer checks.
func TestVerifyTamperedInfoFailsSignature(t *testing.T) {
	t.Parallel()

	_, priv := newKey(t)
	raw := miniTorrent(t)
	signed, err := signing.SignBytes(raw, priv)
	if err != nil {
		t.Fatalf("SignBytes: %v", err)
	}

	// Decode, mutate the info dict's name field, re-encode.
	var mi map[string]bencode.Bytes
	if err := bencode.Unmarshal(signed, &mi); err != nil {
		t.Fatalf("unmarshal signed: %v", err)
	}
	var info map[string]interface{}
	if err := bencode.Unmarshal(mi["info"], &info); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	info["name"] = "different"
	tamperedInfo, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("remarshal info: %v", err)
	}
	mi["info"] = tamperedInfo
	tampered, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("remarshal mi: %v", err)
	}

	_, err = signing.VerifyBytes(tampered)
	if !errors.Is(err, signing.ErrBadSignature) {
		t.Errorf("got %v, want ErrBadSignature", err)
	}
}

// TestSignVerifyFileRoundTrip exercises the on-disk wrappers.
func TestSignVerifyFileRoundTrip(t *testing.T) {
	t.Parallel()

	pub, priv := newKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mini.torrent")
	if err := os.WriteFile(path, miniTorrent(t), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := signing.SignFile(path, priv); err != nil {
		t.Fatalf("SignFile: %v", err)
	}
	sig, err := signing.VerifyFile(path)
	if err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	if string(sig.PubKey[:]) != string(pub) {
		t.Error("pubkey mismatch after round-trip")
	}
}

// TestPubKeyHex verifies the hex encoding.
func TestPubKeyHex(t *testing.T) {
	t.Parallel()

	var sig signing.Signature
	for i := range sig.PubKey {
		sig.PubKey[i] = byte(i)
	}
	got := sig.PubKeyHex()
	want := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
