package reputation_test

import (
	"bytes"
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

func TestBloomAddAndTest(t *testing.T) {
	t.Parallel()
	bf := reputation.NewBloomFilter(1000, 0.01)
	ih := bytes.Repeat([]byte{0x42}, 20)
	if bf.Test(ih) {
		t.Error("fresh filter reports infohash as present")
	}
	bf.Add(ih)
	if !bf.Test(ih) {
		t.Error("after Add, Test returns false")
	}
}

func TestBloomTestAbsentIsRare(t *testing.T) {
	t.Parallel()
	// Add 1000 random infohashes; verify that 10000 unrelated
	// random infohashes have a false-positive rate close to the
	// target 0.01 (with a generous tolerance for the small sample).
	bf := reputation.NewBloomFilter(10_000, 0.01)
	added := make([][]byte, 1000)
	for i := range added {
		ih := make([]byte, 20)
		rand.Read(ih)
		added[i] = ih
		bf.Add(ih)
	}
	for _, ih := range added {
		if !bf.Test(ih) {
			t.Errorf("false negative for added infohash %x", ih)
		}
	}
	const trials = 10000
	var falsePositives int
	for i := 0; i < trials; i++ {
		ih := make([]byte, 20)
		rand.Read(ih)
		// Skip if it happens to collide with an added one (vanishingly rare).
		var collision bool
		for _, a := range added {
			if bytes.Equal(a, ih) {
				collision = true
				break
			}
		}
		if collision {
			continue
		}
		if bf.Test(ih) {
			falsePositives++
		}
	}
	rate := float64(falsePositives) / float64(trials)
	if rate > 0.05 {
		t.Errorf("false positive rate %.4f exceeds 0.05 (target 0.01, configured for 10k items at 0.01)", rate)
	}
}

func TestBloomPersistRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bloom.bin")

	bf, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		ih := bytes.Repeat([]byte{byte(i)}, 20)
		bf.Add(ih)
	}
	if err := bf.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		ih := bytes.Repeat([]byte{byte(i)}, 20)
		if !reopened.Test(ih) {
			t.Errorf("reopened filter missing entry %d", i)
		}
	}
	// Bits and k should match.
	if reopened.Bits() != bf.Bits() {
		t.Errorf("Bits mismatch: %d vs %d", reopened.Bits(), bf.Bits())
	}
	if reopened.HashFunctions() != bf.HashFunctions() {
		t.Errorf("HashFunctions mismatch")
	}
}

func TestBloomPopulationCountAndEstimate(t *testing.T) {
	t.Parallel()
	bf := reputation.NewBloomFilter(1000, 0.01)
	if bf.PopulationCount() != 0 {
		t.Errorf("fresh PopulationCount = %d, want 0", bf.PopulationCount())
	}
	for i := 0; i < 100; i++ {
		ih := bytes.Repeat([]byte{byte(i)}, 20)
		bf.Add(ih)
	}
	pc := bf.PopulationCount()
	if pc == 0 || pc > bf.Bits() {
		t.Errorf("PopulationCount = %d, want non-zero and <= bits %d", pc, bf.Bits())
	}
	est := bf.EstimatedItems()
	if est < 70 || est > 130 {
		t.Errorf("EstimatedItems = %.1f, want roughly 100", est)
	}
}

func TestBloomEmptyPathRejected(t *testing.T) {
	t.Parallel()
	if _, err := reputation.LoadOrCreateBloom(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestBloomAddIsIdempotent(t *testing.T) {
	t.Parallel()
	bf := reputation.NewBloomFilter(1000, 0.01)
	ih := bytes.Repeat([]byte{0x33}, 20)
	bf.Add(ih)
	first := bf.PopulationCount()
	bf.Add(ih)
	second := bf.PopulationCount()
	if first != second {
		t.Errorf("PopulationCount changed on duplicate Add: %d → %d", first, second)
	}
}
