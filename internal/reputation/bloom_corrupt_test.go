package reputation_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBloomRejectsOversizedHeader pins the round-7 fix: a corrupt or crafted
// known-good.bloom whose 24-byte header declares an absurd bit count — but is
// internally consistent (bitsLen == (m+63)/64), so it passes every other guard —
// must be REJECTED with an error (fail-SAFE), never reach make([]uint64, bitsLen)
// and OOM / panic the daemon on startup. loadSpamResistance downgrades the error
// to a warning and runs bloom-less.
func TestBloomRejectsOversizedHeader(t *testing.T) {
	t.Parallel()
	hdr := make([]byte, 24)
	copy(hdr[0:4], "SBLM")
	binary.LittleEndian.PutUint16(hdr[4:6], 1) // version
	binary.LittleEndian.PutUint16(hdr[6:8], 1) // k
	const m = uint64(1) << 60
	binary.LittleEndian.PutUint64(hdr[8:16], m)
	binary.LittleEndian.PutUint64(hdr[16:24], (m+63)/64) // internally consistent

	path := filepath.Join(t.TempDir(), "known-good.bloom")
	if err := os.WriteFile(path, hdr, 0o600); err != nil {
		t.Fatal(err)
	}

	// Must return an error, not panic / OOM. (Without the cap this reaches
	// make([]uint64, 2^54) → makeslice-len panic, crashing rather than erroring.)
	if bf, err := reputation.LoadOrCreateBloom(path); err == nil {
		t.Fatalf("oversized bloom header accepted (bf=%v); want a fail-safe error", bf)
	}
}
