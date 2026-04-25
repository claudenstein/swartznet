package dhtindex_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/signing"
)

// miniTorrent builds a tiny but valid bencoded .torrent-like
// dict the signing package is happy with.
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
	}
	out, err := bencode.Marshal(mi)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return pub, priv
}

// TestPublisherFromMetainfoSignedValid — the happy path: a
// properly signed torrent yields (pubkey, sigValid=true, nil).
func TestPublisherFromMetainfoSignedValid(t *testing.T) {
	t.Parallel()

	pub, priv := newKey(t)
	raw := miniTorrent(t)
	signed, err := signing.SignBytes(raw, priv)
	if err != nil {
		t.Fatalf("SignBytes: %v", err)
	}

	pk, sigValid, err := dhtindex.PublisherFromMetainfo(signed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sigValid {
		t.Error("sigValid = false, want true for a good signature")
	}
	if string(pk[:]) != string(pub) {
		t.Errorf("pubkey mismatch: got %x, want %x", pk[:8], pub[:8])
	}
}

// TestPublisherFromMetainfoUnsigned — the overwhelmingly common
// case. Most mainline torrents have no snet.pubkey; the crawler
// should silently skip them (zero pubkey, sigValid=false, nil).
func TestPublisherFromMetainfoUnsigned(t *testing.T) {
	t.Parallel()

	raw := miniTorrent(t)
	pk, sigValid, err := dhtindex.PublisherFromMetainfo(raw)
	if err != nil {
		t.Fatalf("unexpected error for unsigned metainfo: %v", err)
	}
	if sigValid {
		t.Error("sigValid = true for unsigned metainfo")
	}
	if pk != ([32]byte{}) {
		t.Errorf("pubkey non-zero for unsigned: %x", pk[:8])
	}
}

// TestPublisherFromMetainfoBadSignature — a signed torrent with
// tampered info bytes. We should still return the claimed
// pubkey so Bootstrap can log it, but sigValid=false so
// admission policy doesn't count it.
func TestPublisherFromMetainfoBadSignature(t *testing.T) {
	t.Parallel()

	pub, priv := newKey(t)
	raw := miniTorrent(t)
	signed, err := signing.SignBytes(raw, priv)
	if err != nil {
		t.Fatalf("SignBytes: %v", err)
	}

	// Tamper with info dict so the signature no longer verifies.
	var mi map[string]bencode.Bytes
	if err := bencode.Unmarshal(signed, &mi); err != nil {
		t.Fatalf("unmarshal: %v", err)
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
		t.Fatalf("remarshal: %v", err)
	}

	pk, sigValid, err := dhtindex.PublisherFromMetainfo(tampered)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sigValid {
		t.Error("sigValid = true for tampered torrent")
	}
	if string(pk[:]) != string(pub) {
		t.Errorf("pubkey mismatch on bad-sig path: got %x, want %x", pk[:8], pub[:8])
	}
}

// TestPublisherFromMetainfoMalformed — truncated / garbage
// bencode should surface as an error so operators can spot
// misbehaving DHT peers.
func TestPublisherFromMetainfoMalformed(t *testing.T) {
	t.Parallel()

	garbage := []byte("i'm not bencode at all")
	_, _, err := dhtindex.PublisherFromMetainfo(garbage)
	if err == nil {
		t.Error("expected error for malformed metainfo")
	}
}
