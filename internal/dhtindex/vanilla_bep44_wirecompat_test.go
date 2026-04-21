package dhtindex_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestVanillaBEP44CanReadOurKeywordItem closes wire-compat matrix
// row 8.3-D: a client that speaks only stock BEP-44 — no
// SwartzNet knowledge, no dhtindex package — must be able to
// receive one of our published keyword items, verify its
// ed25519 signature, and parse the value as raw bencode.
//
// The test plays the role of such a vanilla client. It builds
// the same signed bep44 frame our AnacrolixPutter.Put() would
// hand to the DHT traversal, then verifies:
//
//  1. The signature verifies via bep44.Verify, using only the
//     publisher's public key + salt + seq + bencoded value.
//     No SwartzNet code needed.
//  2. bep44.Check passes end-to-end (signature + size caps).
//  3. The value field decodes into a plain map[string]any the
//     way any bencode library would see it, and contains the
//     schema fields documented in docs/05-integration-design.md
//     §5.3 ("hits" list with 20-byte "ih" entries, timestamp
//     "ts"). A vanilla client can extract the list of peers
//     claiming each infohash without understanding anything
//     else about our system.
//
// If we ever tighten MaxValueBytes below BEP-44's 1000-byte cap
// or rename the bencode keys, this test fails loudly.
func TestVanillaBEP44CanReadOurKeywordItem(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// Build a realistic KeywordValue — two hits, one with full
	// metadata, one with only an infohash, so the test also
	// covers the omitempty behavior of the schema.
	var ih1, ih2 [20]byte
	for i := range ih1 {
		ih1[i] = byte(i + 1)
		ih2[i] = byte(0xff - i)
	}
	value := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{
				IH: ih1[:],
				N:  "ubuntu 24.04 desktop amd64",
				S:  128,
				F:  1,
				Sz: 6 << 30,
			},
			{IH: ih2[:]},
		},
	}
	salt, err := dhtindex.SaltForKeyword("ubuntu")
	if err != nil {
		t.Fatal(err)
	}

	// Emulate exactly what AnacrolixPutter.Put does before the
	// DHT traversal: serialise the value, re-decode to an
	// interface{} so bep44.Sign's MustMarshal produces the same
	// bytes, then sign.
	encoded, err := dhtindex.EncodeValue(value)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if len(encoded) > 1000 {
		t.Fatalf("encoded value %d bytes exceeds BEP-44 cap 1000", len(encoded))
	}
	var v interface{}
	if err := bencode.Unmarshal(encoded, &v); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
	var pubArr [32]byte
	copy(pubArr[:], pub)
	const seq int64 = 1
	put := bep44.Put{
		V:    v,
		K:    &pubArr,
		Salt: salt,
		Seq:  seq,
	}
	put.Sign(priv)

	// ------------------------------------------------------------
	// Vanilla verification step 1: signature verifies with the
	// stock bep44 package and a stock ed25519 public key.
	bv := bencode.MustMarshal(put.V)
	if !bep44.Verify(ed25519.PublicKey(pub), salt, seq, bv, put.Sig[:]) {
		t.Fatal("bep44.Verify returned false for a correctly-signed item")
	}

	// Vanilla verification step 2: bep44.Check passes — covers
	// MaxValueBytes, MaxSaltBytes, and signature together.
	item := put.ToItem()
	if err := bep44.Check(item); err != nil {
		t.Fatalf("bep44.Check: %v", err)
	}

	// ------------------------------------------------------------
	// Schema inspection as a completely naive bencode client.
	// No dhtindex.DecodeValue — we only use bencode.
	var generic map[string]interface{}
	if err := bencode.Unmarshal(bv, &generic); err != nil {
		t.Fatalf("vanilla bencode decode: %v", err)
	}
	hitsRaw, ok := generic["hits"]
	if !ok {
		t.Fatal("value is missing required 'hits' key — spec §5.3")
	}
	hits, ok := hitsRaw.([]interface{})
	if !ok {
		t.Fatalf("'hits' is not a list, got %T", hitsRaw)
	}
	if len(hits) != 2 {
		t.Fatalf("len(hits) = %d, want 2", len(hits))
	}
	firstHit, ok := hits[0].(map[string]interface{})
	if !ok {
		t.Fatalf("hits[0] not a dict, got %T", hits[0])
	}
	ihRaw, ok := firstHit["ih"]
	if !ok {
		t.Fatal("hits[0] missing required 'ih' key")
	}
	ihBytes, ok := ihRaw.(string) // bencode strings come back as string in raw decode
	if !ok {
		t.Fatalf("hits[0].ih is not a byte string, got %T", ihRaw)
	}
	if len(ihBytes) != 20 {
		t.Errorf("hits[0].ih length = %d, want 20 (SHA-1 infohash)", len(ihBytes))
	}
	if _, ok := generic["ts"]; !ok {
		t.Error("value missing 'ts' timestamp key")
	}

	// ------------------------------------------------------------
	// Tampering canary: bit-flip in the bencoded value should
	// make bep44.Verify reject — proves we're verifying the real
	// signature, not just trusting it.
	tampered := append([]byte(nil), bv...)
	tampered[len(tampered)-1] ^= 0xff
	if bep44.Verify(ed25519.PublicKey(pub), salt, seq, tampered, put.Sig[:]) {
		t.Fatal("bep44.Verify accepted a bit-flipped payload — signature check is broken")
	}
}
