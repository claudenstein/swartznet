package dhtschema_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// TestEncodeValueGoldenVector pins the frozen wire bytes for a fully-populated
// KeywordValue. If this changes, every published DHT item changes target/shape
// and back-compat with ≥12-month-old readers breaks. The bencode key order is
// lexicographic (hits, ts; and within a hit f, ih, n, s, sz) — part of the
// contract, not an implementation detail.
func TestEncodeValueGoldenVector(t *testing.T) {
	t.Parallel()
	var ih1, ih2 [20]byte
	for i := range ih1 {
		ih1[i] = byte(i + 1)
		ih2[i] = byte(0xff - i)
	}
	v := dhtschema.KeywordValue{
		Ts: 1700000000,
		Hits: []dhtschema.KeywordHit{
			{IH: ih1[:], N: "ubuntu 24.04 desktop amd64", S: 128, F: 1, Sz: 6 << 30},
			{IH: ih2[:]},
		},
	}
	const golden = "64343a686974736c64313a66693165323a696832303a0102030405060708090a0b0c0d0e0f1011121314313a6e32363a7562756e74752032342e3034206465736b746f7020616d643634313a736931323865323a737a6936343432343530393434656564323a696832303afffefdfcfbfaf9f8f7f6f5f4f3f2f1f0efeeedec6565323a747369313730303030303030306565"
	out, err := dhtschema.EncodeValue(v)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if got := hex.EncodeToString(out); got != golden {
		t.Errorf("golden vector drift:\n got %s\nwant %s", got, golden)
	}
}

// TestEncodeValueEmptyGolden pins the empty-hits form and confirms EncodeValue
// normalizes nil Hits to an empty bencode list.
func TestEncodeValueEmptyGolden(t *testing.T) {
	t.Parallel()
	out, err := dhtschema.EncodeValue(dhtschema.KeywordValue{Ts: 1700000000})
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if got := string(out); got != "d4:hitsle2:tsi1700000000ee" {
		t.Errorf("empty golden = %q", got)
	}
}

// TestEncodeBep44RemarshalIdentity guards the load-bearing round-trip: the put
// path re-decodes EncodeValue output to an interface{} and hands it to
// bep44.Put.Sign, which re-marshals it. If that re-marshal is not byte-
// identical the signature covers different bytes than were published and every
// vanilla verify fails.
func TestEncodeBep44RemarshalIdentity(t *testing.T) {
	t.Parallel()
	v := dhtschema.KeywordValue{
		Hits: []dhtschema.KeywordHit{{IH: bytes.Repeat([]byte{0xab}, 20), N: "ubuntu", S: 100, F: 12, Sz: 6 << 30}},
	}
	out, err := dhtschema.EncodeValue(v)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	var iface interface{}
	if err := bencode.Unmarshal(out, &iface); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	re, err := bencode.Marshal(iface)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(re, out) {
		t.Errorf("bep44 re-marshal not byte-identical:\n got %x\nwant %x", re, out)
	}
}

// TestEncodeDecodeRoundTrip is the basic sanity round-trip.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	orig := dhtschema.KeywordValue{
		Hits: []dhtschema.KeywordHit{
			{IH: bytes.Repeat([]byte{0x01}, 20), N: "ubuntu", S: 100, F: 12, Sz: 6 << 30},
			{IH: bytes.Repeat([]byte{0x02}, 20), N: "ubuntu desktop", S: 50, F: 4, Sz: 4 << 30},
		},
	}
	encoded, err := dhtschema.EncodeValue(orig)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	decoded, err := dhtschema.DecodeValue(encoded)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if decoded.Ts == 0 {
		t.Error("decoded.Ts is zero; EncodeValue should auto-fill it")
	}
	if len(decoded.Hits) != 2 || !bytes.Equal(decoded.Hits[0].IH, orig.Hits[0].IH) ||
		decoded.Hits[1].N != "ubuntu desktop" {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}

// TestEncodeValueHitsNilDefaults covers the nil-Hits normalization arm.
func TestEncodeValueHitsNilDefaults(t *testing.T) {
	t.Parallel()
	encoded, err := dhtschema.EncodeValue(dhtschema.KeywordValue{})
	if err != nil {
		t.Fatalf("EncodeValue zero value: %v", err)
	}
	decoded, err := dhtschema.DecodeValue(encoded)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if decoded.Ts == 0 {
		t.Error("decoded.Ts is zero; EncodeValue should auto-fill it")
	}
	if len(decoded.Hits) != 0 {
		t.Errorf("decoded.Hits len = %d, want 0", len(decoded.Hits))
	}
}

// TestEncodeValueCap: a value that encodes to over 1000 bytes is rejected.
func TestEncodeValueCap(t *testing.T) {
	t.Parallel()
	v := dhtschema.KeywordValue{}
	for i := 0; i < 100; i++ {
		v.Hits = append(v.Hits, dhtschema.KeywordHit{
			IH: bytes.Repeat([]byte{byte(i)}, 20),
			N:  strings.Repeat("x", 60),
		})
	}
	if _, err := dhtschema.EncodeValue(v); err == nil {
		t.Fatal("expected error for oversized value")
	} else if !strings.Contains(err.Error(), "exceeds BEP-44 cap") {
		t.Errorf("error = %q, want it to mention 'exceeds BEP-44 cap'", err.Error())
	}
}

// TestDecodeValueRejectsOversizeBeforeUnmarshal: a payload larger than the cap
// (which a malicious node can return up to the UDP datagram limit) is rejected
// before it drives an allocation.
func TestDecodeValueRejectsOversizeBeforeUnmarshal(t *testing.T) {
	t.Parallel()
	var hits []dhtschema.KeywordHit
	for i := 0; i < 200; i++ {
		hits = append(hits, dhtschema.KeywordHit{
			IH: bytes.Repeat([]byte{byte(i)}, 20),
			N:  strings.Repeat("a", 20),
		})
	}
	payload, err := bencode.Marshal(dhtschema.KeywordValue{Ts: 1700000000, Hits: hits})
	if err != nil {
		t.Fatalf("marshal oversize: %v", err)
	}
	if len(payload) <= dhtschema.MaxValueBytes {
		t.Fatalf("test payload %d bytes not over cap %d", len(payload), dhtschema.MaxValueBytes)
	}
	if _, err := dhtschema.DecodeValue(payload); err == nil {
		t.Errorf("DecodeValue accepted a %d-byte payload over the cap", len(payload))
	}
}

// TestDecodeValueAcceptsAtCap: the cap is inclusive.
func TestDecodeValueAcceptsAtCap(t *testing.T) {
	t.Parallel()
	payload, err := bencode.Marshal(dhtschema.KeywordValue{
		Ts:   1700000000,
		Hits: []dhtschema.KeywordHit{{IH: bytes.Repeat([]byte{1}, 20), N: "u"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) > dhtschema.MaxValueBytes {
		t.Fatalf("fixture %d bytes already over cap", len(payload))
	}
	if _, err := dhtschema.DecodeValue(payload); err != nil {
		t.Errorf("DecodeValue rejected an in-cap payload: %v", err)
	}
}

// TestDecodeEmpty: nil payload errors.
func TestDecodeEmpty(t *testing.T) {
	t.Parallel()
	if _, err := dhtschema.DecodeValue(nil); err == nil {
		t.Error("expected error for nil payload")
	}
}

// TestEstimateValueSizeStampsTs pins the headroom fix: the estimate must
// reflect the ~10-digit live timestamp EncodeValue injects, never a Ts==0
// sentinel, so a near-cap entry that passes eviction also survives the live
// encode.
func TestEstimateValueSizeStampsTs(t *testing.T) {
	t.Parallel()
	v := dhtschema.KeywordValue{
		Hits: []dhtschema.KeywordHit{{IH: bytes.Repeat([]byte{1}, 20), N: "ubuntu"}},
	}
	est := dhtschema.EstimateValueSize(v)
	encoded, err := dhtschema.EncodeValue(v)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if est < len(encoded) {
		t.Errorf("EstimateValueSize=%d < live EncodeValue=%d; estimate must include the timestamp width",
			est, len(encoded))
	}
}

// TestSaltForKeywordVerbatim: SaltForKeyword returns bytes verbatim (no
// lowercasing), errors on empty, errors over the 64-byte cap.
func TestSaltForKeywordVerbatim(t *testing.T) {
	t.Parallel()
	salt, err := dhtschema.SaltForKeyword("ubuntu")
	if err != nil || string(salt) != "ubuntu" {
		t.Errorf("salt = %q, err = %v", salt, err)
	}
	// Verbatim: mixed case is preserved (tokenizer output is already lower).
	mixed, err := dhtschema.SaltForKeyword("UbuntU")
	if err != nil || string(mixed) != "UbuntU" {
		t.Errorf("SaltForKeyword must not lowercase: got %q", mixed)
	}
	if _, err := dhtschema.SaltForKeyword(""); err == nil {
		t.Error("expected error for empty keyword")
	}
	if _, err := dhtschema.SaltForKeyword(strings.Repeat("x", dhtschema.MaxSaltBytes+1)); err == nil {
		t.Error("expected error for oversized keyword")
	}
}

// TestSaltForShard: shard 0 is the bare keyword, shards 1+ append "#<n>".
func TestSaltForShard(t *testing.T) {
	t.Parallel()
	zero, _ := dhtschema.SaltForShard("ubuntu", 0)
	first, _ := dhtschema.SaltForShard("ubuntu", 1)
	second, _ := dhtschema.SaltForShard("ubuntu", 2)
	if string(zero) != "ubuntu" || string(first) != "ubuntu#1" || string(second) != "ubuntu#2" {
		t.Errorf("shards: %q %q %q", zero, first, second)
	}
}

// TestDecodeLegacyKeywordValue is the ≥12-month back-compat gate: bytes frozen
// from the legacy dhtindex.EncodeValue path must still decode to the same
// fields. (Same golden as TestEncodeValueGoldenVector, read from the decode
// side to prove old-writer → new-reader compatibility.)
func TestDecodeLegacyKeywordValue(t *testing.T) {
	t.Parallel()
	const golden = "64343a686974736c64313a66693165323a696832303a0102030405060708090a0b0c0d0e0f1011121314313a6e32363a7562756e74752032342e3034206465736b746f7020616d643634313a736931323865323a737a6936343432343530393434656564323a696832303afffefdfcfbfaf9f8f7f6f5f4f3f2f1f0efeeedec6565323a747369313730303030303030306565"
	raw, err := hex.DecodeString(golden)
	if err != nil {
		t.Fatal(err)
	}
	v, err := dhtschema.DecodeValue(raw)
	if err != nil {
		t.Fatalf("DecodeValue(legacy golden): %v", err)
	}
	if v.Ts != 1700000000 || len(v.Hits) != 2 {
		t.Fatalf("legacy decode mismatch: ts=%d hits=%d", v.Ts, len(v.Hits))
	}
	if v.Hits[0].N != "ubuntu 24.04 desktop amd64" || v.Hits[0].S != 128 ||
		v.Hits[0].F != 1 || v.Hits[0].Sz != 6<<30 {
		t.Errorf("legacy hit[0] mismatch: %+v", v.Hits[0])
	}
	if len(v.Hits[1].IH) != 20 || v.Hits[1].N != "" {
		t.Errorf("legacy hit[1] mismatch: %+v", v.Hits[1])
	}
}
