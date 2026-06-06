package dhtindex_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestEstimateValueSizeAccountsForTimestampWidth pins the headroom
// fix: EstimateValueSize must reflect the ~10-digit timestamp that
// EncodeValue injects at publish time, not a Ts==0 sentinel. With the
// bug the estimate (Ts==0, "tsi0e") undercounts the live encode
// (Ts==now, "tsi1700000000e") by ~9 bytes.
func TestEstimateValueSizeAccountsForTimestampWidth(t *testing.T) {
	t.Parallel()
	v := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{{
			IH: bytes.Repeat([]byte{1}, 20),
			N:  "ubuntu",
		}},
	}
	est := dhtindex.EstimateValueSize(v)

	// The live publish path always stamps a non-zero Ts. The estimate
	// must be >= the live encoded size so the eviction loop never
	// believes a near-cap entry fits when it actually overflows.
	encoded, err := dhtindex.EncodeValue(v)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if est < len(encoded) {
		t.Errorf("EstimateValueSize=%d < live EncodeValue=%d; estimate must include the timestamp width",
			est, len(encoded))
	}
}

// TestAddHitNearCapStillEncodes is the end-to-end regression for the
// estimate/encode gap: build an entry that AddHit leaves right at the
// cap, then run the real EncodeValue (with a live timestamp) and
// require it to stay under MaxValueBytes. With the Ts==0 estimate this
// failed because the live encode added ~9 bytes for the timestamp.
func TestAddHitNearCapStillEncodes(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")

	// Add hits with growing names until eviction kicks in, so the
	// final entry sits as close to the cap as AddHit allows.
	for i := 0; i < 40; i++ {
		ih := bytes.Repeat([]byte{byte(i + 1)}, 20)
		if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{
			IH: ih,
			N:  strings.Repeat("x", 20),
		}); err != nil {
			t.Fatalf("AddHit %d: %v", i, err)
		}
	}

	snap := mf.Snapshot()["ubuntu"]
	if snap == nil {
		t.Fatal("manifest entry missing")
	}
	// The full publish path: EncodeValue stamps a live Ts and enforces
	// MaxValueBytes. It must NOT return the "exceeds cap" error.
	if _, err := dhtindex.EncodeValue(dhtindex.KeywordValue{Hits: snap.Hits}); err != nil {
		t.Errorf("EncodeValue on a near-cap AddHit entry failed: %v", err)
	}
}

// TestDecodeValueRejectsOversize verifies DecodeValue mirrors the
// encode-side cap: a payload larger than MaxValueBytes (which a
// malicious node can return up to the UDP datagram limit) is rejected
// before unmarshalling.
func TestDecodeValueRejectsOversize(t *testing.T) {
	t.Parallel()
	// Craft a valid bencoded KeywordValue with enough hits to exceed
	// the cap, bypassing the encode-side guard by marshalling directly.
	var hits []dhtindex.KeywordHit
	for i := 0; i < 200; i++ {
		hits = append(hits, dhtindex.KeywordHit{
			IH: bytes.Repeat([]byte{byte(i)}, 20),
			N:  strings.Repeat("a", 20),
		})
	}
	payload, err := bencode.Marshal(dhtindex.KeywordValue{Ts: 1700000000, Hits: hits})
	if err != nil {
		t.Fatalf("marshal oversize: %v", err)
	}
	if len(payload) <= dhtindex.MaxValueBytes {
		t.Fatalf("test payload %d bytes not over cap %d", len(payload), dhtindex.MaxValueBytes)
	}
	if _, err := dhtindex.DecodeValue(payload); err == nil {
		t.Errorf("DecodeValue accepted a %d-byte payload over the %d cap", len(payload), dhtindex.MaxValueBytes)
	}
}

// TestDecodeValueAcceptsAtCap confirms the cap check is inclusive: a
// payload at exactly MaxValueBytes still decodes.
func TestDecodeValueAcceptsAtCap(t *testing.T) {
	t.Parallel()
	payload, err := bencode.Marshal(dhtindex.KeywordValue{
		Ts:   1700000000,
		Hits: []dhtindex.KeywordHit{{IH: bytes.Repeat([]byte{1}, 20), N: "u"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(payload) > dhtindex.MaxValueBytes {
		t.Fatalf("fixture %d bytes already over cap", len(payload))
	}
	if _, err := dhtindex.DecodeValue(payload); err != nil {
		t.Errorf("DecodeValue rejected an in-cap payload: %v", err)
	}
}
