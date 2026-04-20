package companion_test

import (
	"bytes"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

// TestEncodeNilTorrentsDefaultsToEmptySlice covers the
// `idx.Torrents == nil` short-circuit in Encode: callers may pass
// a CompanionIndex with a nil Torrents slice, and the encoder must
// substitute an empty slice so the JSON contains "torrents":[]
// rather than "torrents":null. Subscribers rely on this.
func TestEncodeNilTorrentsDefaultsToEmptySlice(t *testing.T) {
	t.Parallel()
	idx := companion.CompanionIndex{Publisher: "abcd"} // Torrents nil
	encoded, err := companion.Encode(idx)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := companion.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Torrents == nil {
		t.Error("decoded Torrents is nil; Encode/Decode should normalise to []")
	}
	if len(got.Torrents) != 0 {
		t.Errorf("decoded Torrents = %d, want 0", len(got.Torrents))
	}
}

// TestNewSubscriberWorkerRejectsNil covers the documented nil-
// subscriber guard.
func TestNewSubscriberWorkerRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := companion.NewSubscriberWorker(nil); err == nil {
		t.Error("NewSubscriberWorker(nil) should error")
	}
}
