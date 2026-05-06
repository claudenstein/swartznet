package dhtindex

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// TestPointerTimestampRoundTrip locks in the wire shape of the
// BEP-46 pointer with the new ts field. A round-trip through
// bencode.Marshal/Unmarshal must preserve both fields, and ts
// must marshal as a present-but-omittable int64.
func TestPointerTimestampRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().Unix()
	in := bep46Pointer{IH: make([]byte, 20), TS: now}
	for i := range in.IH {
		in.IH[i] = byte(i)
	}

	encoded, err := bencode.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out bep46Pointer
	if err := bencode.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(out.IH) != string(in.IH) {
		t.Fatalf("IH round-trip: got %x, want %x", out.IH, in.IH)
	}
	if out.TS != now {
		t.Fatalf("TS round-trip: got %d, want %d", out.TS, now)
	}
}

// TestPointerTimestampOmitemptyOnZero proves that a zero TS
// does NOT appear in the encoded output. Without omitempty, a
// publisher would emit `{ih: ..., ts: 0}` which a downstream
// might mistakenly read as "this pointer is from epoch zero
// (1970)" — i.e. ancient and stale — instead of "unknown
// freshness". The omitempty tag avoids that confusion: zero
// values are legitimately absent on the wire.
func TestPointerTimestampOmitemptyOnZero(t *testing.T) {
	t.Parallel()
	in := bep46Pointer{IH: make([]byte, 20)} // TS unset → 0
	encoded, err := bencode.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(encoded); contains(got, "ts") {
		t.Fatalf("encoded form contains ts key for zero TS: %q", got)
	}
}

// TestPointerLegacyDecodeTolerant proves that values written
// by pre-ts publishers (`{ih: ...}` only) decode cleanly into
// the new struct, with TS=0 marking unknown freshness. This
// is the wire-compat guarantee that lets us roll out the new
// field without coordinating with deployed subscribers.
func TestPointerLegacyDecodeTolerant(t *testing.T) {
	t.Parallel()
	// Bencoded shape of {ih: <20 bytes of 0xAB>}.
	legacy := []byte("d2:ih20:")
	for i := 0; i < 20; i++ {
		legacy = append(legacy, 0xAB)
	}
	legacy = append(legacy, 'e')

	var out bep46Pointer
	if err := bencode.Unmarshal(legacy, &out); err != nil {
		t.Fatalf("legacy Unmarshal: %v", err)
	}
	if len(out.IH) != 20 {
		t.Fatalf("IH len after legacy decode: got %d, want 20", len(out.IH))
	}
	if out.TS != 0 {
		t.Fatalf("TS after legacy decode: got %d, want 0", out.TS)
	}
}

// TestPointerForwardCompatExtraField proves that values written
// by FUTURE publishers carrying additional dict entries
// (e.g. a hypothetical `nonce` or `nick` field) still decode
// cleanly into the current struct, ignoring the unknown keys.
// This locks in bencode's extensibility: subscribers running
// today won't break when tomorrow's publishers add more.
func TestPointerForwardCompatExtraField(t *testing.T) {
	t.Parallel()
	// Bencoded shape of {extra: 7, ih: <20 0xCC>, ts: 99}.
	// Keys are sorted lexicographically per bencode rules.
	val := []byte("d5:extrai7e2:ih20:")
	for i := 0; i < 20; i++ {
		val = append(val, 0xCC)
	}
	val = append(val, "2:tsi99e"...)
	val = append(val, 'e')

	var out bep46Pointer
	if err := bencode.Unmarshal(val, &out); err != nil {
		t.Fatalf("forward-compat Unmarshal: %v", err)
	}
	if out.TS != 99 {
		t.Fatalf("TS: got %d, want 99", out.TS)
	}
	if len(out.IH) != 20 || out.IH[0] != 0xCC {
		t.Fatalf("IH: got %x, want 20 bytes of 0xCC", out.IH)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
