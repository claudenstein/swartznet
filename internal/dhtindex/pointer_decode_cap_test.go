package dhtindex

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/anacrolix/dht/v2/traversal"
)

// TestDecodePointerValueBounds covers decodePointerValue, the
// validation seam GetInfohashPointer feeds remote-supplied BEP-46
// values through. The value is signature-bound but still untrusted
// publisher input: anything over MaxValueBytes must be rejected
// before the bencode decoder runs, and the ih field must be exactly
// 20 bytes.
func TestDecodePointerValueBounds(t *testing.T) {
	t.Parallel()

	ih := bytes.Repeat([]byte{0xD7}, 20)
	valid := fmt.Sprintf("d2:ih20:%se", ih)
	// Oversized-but-otherwise-decodable: a dict with a valid 20-byte
	// ih plus a padding key pushing the whole value past the BEP-44
	// cap. Proves the size check fires before (not instead of) the
	// decode — without the cap this input would decode successfully.
	pad := strings.Repeat("x", MaxValueBytes)
	oversized := fmt.Sprintf("d2:ih20:%s3:pad%d:%se", ih, len(pad), pad)

	cases := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"valid", valid, ""},
		{"oversized", oversized, "exceeds BEP-44 cap"},
		{"garbage", "not bencode at all", "decode pointer"},
		{"short-ih", "d2:ih5:shorte", "want 20"},
		{"long-ih", fmt.Sprintf("d2:ih21:%sXe", ih), "want 20"},
		{"empty", "", "decode pointer"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodePointerValue([]byte(tc.raw))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("decodePointerValue: %v", err)
				}
				if !bytes.Equal(got.InfoHash[:], ih) {
					t.Errorf("infohash = %x, want %x", got.InfoHash, ih)
				}
				return
			}
			if err == nil {
				t.Fatalf("decodePointerValue accepted %q, want error containing %q", tc.name, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestCheckPutStatsZeroNodes locks in the shared zero-node assertion
// every put path (keyword, BEP-46 pointer, PPMI) runs after
// getput.Put. nil stats and zero responses both mean the item never
// landed and must fail closed.
func TestCheckPutStatsZeroNodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		stats   *traversal.Stats
		wantErr bool
	}{
		{"nil-stats", nil, true},
		{"zero-responses", &traversal.Stats{}, true},
		{"one-response", &traversal.Stats{NumResponses: 1}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkPutStats(tc.stats, "put")
			if tc.wantErr && err == nil {
				t.Fatal("checkPutStats returned nil for a zero-node put")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("checkPutStats: %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "zero DHT nodes") {
				t.Errorf("error = %q, want the zero-node message", err)
			}
		})
	}
}
