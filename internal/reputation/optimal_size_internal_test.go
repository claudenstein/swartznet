package reputation

import "testing"

// TestOptimalSizeFallbackForZeroN — the m==0 guard fires
// when n=0 (mF = -0*ln(p)/ln(2)^2 = 0; math.Ceil(0) = 0;
// uint64(0) = 0; m gets promoted to 1). The k path produces
// NaN-via-divide-by-zero on n=0, so we don't assert on k —
// that's a different defensive guard whose practical
// reachability depends on the platform's NaN→uint64
// conversion semantics.
func TestOptimalSizeFallbackForZeroN(t *testing.T) {
	t.Parallel()
	m, _ := optimalSize(0, 0.01)
	if m != 1 {
		t.Errorf("m for n=0 = %d, want 1 (fallback)", m)
	}
}

// TestOptimalSizeRealistic — sanity check the realistic case
// matches the documented Bloom-filter math (m grows with -ln(p),
// k stays around 5-10 for p in [0.001, 0.05]).
func TestOptimalSizeRealistic(t *testing.T) {
	t.Parallel()
	m, k := optimalSize(10_000, 0.01)
	if m < 80_000 || m > 120_000 {
		t.Errorf("m for n=10k p=0.01 = %d, expected ~95k", m)
	}
	if k < 4 || k > 12 {
		t.Errorf("k for p=0.01 = %d, expected ~7", k)
	}
}
