package dhtindex

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/torrent/bencode"
)

// TestBep46PointerSignVerifyRoundtrip proves the typed-struct
// → interface{} → sign → verify round-trip used by
// AnacrolixPutter.PutInfohashPointer produces a signature that
// bep44.Check will accept. If this test fails, every BEP-44
// put in the codebase is silently rejected by the receiver's
// signature validator, which is a catastrophic latent bug
// because: (a) token validation passes (so the sender sees
// nothing wrong), (b) store.Put errors aren't reported via any
// expvar, (c) the only visible symptom is "BEP-44 get returns
// value not found".
//
// Mirrors the exact sequence in PutInfohashPointer:
// marshal-struct → unmarshal-interface → put.Sign → marshal
// again inside Check. Any mismatch at any step flags the
// issue.
func TestBep46PointerSignVerifyRoundtrip(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	var pubArr [32]byte
	copy(pubArr[:], pub)

	var ih [20]byte
	for i := range ih {
		ih[i] = 0xBE
	}
	salt := []byte("roundtrip-salt")

	// Step 1: marshal typed struct (what PutInfohashPointer does).
	v := bep46Pointer{IH: ih[:]}
	encodedStruct, err := bencode.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	// Step 2: decode to interface{} (sign path input).
	var decoded interface{}
	if err := bencode.Unmarshal(encodedStruct, &decoded); err != nil {
		t.Fatal(err)
	}

	// Step 3: re-marshal the interface; this is what Put.Sign and
	// bep44.Check both do internally.
	encodedIface, err := bencode.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encodedStruct, encodedIface) {
		t.Errorf("bencode round-trip diverged:\n  struct: %q\n  iface:  %q",
			encodedStruct, encodedIface)
	}

	// Step 4: sign and verify against the interface form (what the
	// wire-format receiver would reproduce).
	put := &bep44.Put{
		V:    decoded,
		K:    &pubArr,
		Salt: salt,
		Seq:  1,
	}
	put.Sign(priv)

	item := put.ToItem()
	if err := bep44.Check(item); err != nil {
		t.Fatalf("bep44.Check rejected our signed item: %v", err)
	}
}
