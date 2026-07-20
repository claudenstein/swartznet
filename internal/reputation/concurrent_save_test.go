package reputation_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestConcurrentTrackerSaveNoCorruption pins the atomic-write fix: many
// concurrent mutate+Save cycles must never publish a corrupt file. Before
// Save used a UNIQUE os.CreateTemp file, two saves that snapshotted
// different-length JSON shared a fixed "<path>.tmp" inode; the shorter
// write left the longer write's tail, and rename published the torn,
// invalid document — losing every counter on the next load.
func TestConcurrentTrackerSaveNoCorruption(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "tracker.json")
	tr, err := reputation.LoadOrCreateTracker(path)
	if err != nil {
		t.Fatalf("LoadOrCreateTracker: %v", err)
	}

	const workers, iters = 8, 40
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				pk := reputation.PubKeyHex(fmt.Sprintf("%064x", w*iters+i))
				tr.RecordReturned(pk, 1)
				if err := tr.Save(); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// The published file must always be reloadable and valid JSON.
	if _, err := reputation.LoadOrCreateTracker(path); err != nil {
		t.Fatalf("reload after concurrent saves failed (corrupt file?): %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("published tracker.json is not valid JSON: %v", err)
	}
}

// TestConcurrentBloomSaveNoCorruption is the Bloom analogue. The Bloom is
// fixed-length so it is more self-healing, but a shared-tmp tear could
// still corrupt the header/bitsLen; unique tempfiles rule that out.
func TestConcurrentBloomSaveNoCorruption(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "known-good.bloom")
	bf, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatalf("LoadOrCreateBloom: %v", err)
	}

	// The default Bloom is ~1.2 MB, so keep the write count modest — the
	// shared-tmp tear the fix rules out needs only two overlapping saves.
	const workers, iters = 4, 4
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				bf.Add([]byte(fmt.Sprintf("ih-%d-%d", w, i)))
				if err := bf.Save(); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if _, err := reputation.LoadOrCreateBloom(path); err != nil {
		t.Fatalf("reload after concurrent saves failed (corrupt file?): %v", err)
	}
}
