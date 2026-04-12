package dhtindex_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestRegtestPublisherOptionsShrinksIntervals pins down the
// M15a regtest-mode time constants. If someone tweaks them
// upward by accident (e.g. "just make the test a bit more
// realistic"), the test fails with a clear message pointing
// at the regtest contract.
func TestRegtestPublisherOptionsShrinksIntervals(t *testing.T) {
	t.Parallel()
	prod := dhtindex.DefaultPublisherOptions()
	reg := dhtindex.RegtestPublisherOptions()

	// Every time constant must be strictly smaller in regtest.
	if reg.RefreshInterval >= prod.RefreshInterval {
		t.Errorf("regtest RefreshInterval %v >= production %v; regtest must be faster",
			reg.RefreshInterval, prod.RefreshInterval)
	}
	if reg.MinPutInterval >= prod.MinPutInterval {
		t.Errorf("regtest MinPutInterval %v >= production %v; regtest must be faster",
			reg.MinPutInterval, prod.MinPutInterval)
	}

	// Sanity floors — a scenario test still needs *some*
	// minimum interval or the worker burns CPU for no gain.
	if reg.RefreshInterval < 100*time.Millisecond {
		t.Errorf("regtest RefreshInterval %v too small (<100ms); burns CPU",
			reg.RefreshInterval)
	}
	if reg.MinPutInterval < 10*time.Millisecond {
		t.Errorf("regtest MinPutInterval %v too small (<10ms)",
			reg.MinPutInterval)
	}

	// Sanity ceilings — if regtest intervals exceed 60s, a
	// scenario test waiting for a publish tick would be slow
	// enough to defeat the purpose.
	if reg.RefreshInterval > 60*time.Second {
		t.Errorf("regtest RefreshInterval %v too large (>60s); defeats the point",
			reg.RefreshInterval)
	}
}
