package dhtindex

import (
	"math"
	"testing"
)

// TestNextSeqClampsAtMaxInt64 locks in the BEP-44 sequence-
// number overflow guard. The closure signature in AnacrolixPutter
// has no error path so a wrap to negative would silently violate
// BEP-44 monotonicity. We clamp instead.
func TestNextSeqClampsAtMaxInt64(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   int64
		want int64
	}{
		{"zero", 0, 1},
		{"one", 1, 2},
		{"million", 1_000_000, 1_000_001},
		{"max-minus-one", math.MaxInt64 - 1, math.MaxInt64},
		{"max", math.MaxInt64, math.MaxInt64},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := nextSeq(tc.in)
			if got != tc.want {
				t.Fatalf("nextSeq(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
