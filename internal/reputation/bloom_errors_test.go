package reputation_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBloomSaveRenameFailure covers the rename-failure branch of
// BloomFilter.Save. Same strategy as the tracker test: bind to a
// path that does not yet exist, then replace it with a non-empty
// directory so os.Rename can't replace it with the regular tempfile.
func TestBloomSaveRenameFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bloom.bin")

	bf, err := reputation.LoadOrCreateBloom(path)
	if err != nil {
		t.Fatalf("LoadOrCreateBloom: %v", err)
	}
	bf.Add([]byte("ubuntu-2404"))

	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := bf.Save(); err == nil {
		t.Error("Save should fail when the target path is a non-empty directory")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tempfile leaked: stat err = %v", err)
	}
}

// TestLoadOrCreateBloomOnDirectoryFailsToOpen covers the
// non-ErrNotExist os.Open error branch. Pointing the path at a
// directory makes os.Open succeed (you can open a directory) but
// readBloom then fails on the very first ReadFull (directory
// reads return EISDIR-style errors). Either way LoadOrCreateBloom
// must return an error rather than synthesise a fresh filter.
func TestLoadOrCreateBloomOnDirectoryFailsToOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := reputation.LoadOrCreateBloom(dir); err == nil {
		t.Error("LoadOrCreateBloom on a directory path should error")
	}
}

// TestLoadOrCreateBloomBadMagic covers the readBloom magic-mismatch
// error branch. The stored file has the wrong 4-byte prefix so the
// decoder must reject it.
func TestLoadOrCreateBloomBadMagic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bloom.bin")

	// Write a header with bogus magic + plausible everything else so
	// the failure pinpoints the magic check, not a length check.
	var hdr [4 + 2 + 2 + 8 + 8]byte
	copy(hdr[0:4], "XXXX")
	binary.LittleEndian.PutUint16(hdr[4:6], 1)
	binary.LittleEndian.PutUint16(hdr[6:8], 7) // k
	binary.LittleEndian.PutUint64(hdr[8:16], 64)
	binary.LittleEndian.PutUint64(hdr[16:24], 1)
	if err := os.WriteFile(path, hdr[:], 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := reputation.LoadOrCreateBloom(path); err == nil {
		t.Error("LoadOrCreateBloom with bad magic should error")
	}
}

// TestLoadOrCreateBloomBadVersion covers the version-check branch
// in readBloom: correct magic but a version the decoder does not
// recognise.
func TestLoadOrCreateBloomBadVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bloom.bin")

	var hdr [4 + 2 + 2 + 8 + 8]byte
	copy(hdr[0:4], "SBLM")                          // valid magic
	binary.LittleEndian.PutUint16(hdr[4:6], 0xFFFF) // unsupported version
	binary.LittleEndian.PutUint16(hdr[6:8], 7)
	binary.LittleEndian.PutUint64(hdr[8:16], 64)
	binary.LittleEndian.PutUint64(hdr[16:24], 1)
	if err := os.WriteFile(path, hdr[:], 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := reputation.LoadOrCreateBloom(path); err == nil {
		t.Error("LoadOrCreateBloom with unsupported version should error")
	}
}

// TestLoadOrCreateBloomZeroParams covers the m==0 / k==0 guard in
// readBloom. A corrupt header with m=0 would otherwise produce a
// filter whose first Test/Add divides by zero and panics; the loader
// must instead fail closed with a recoverable error.
func TestLoadOrCreateBloomZeroParams(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		k    uint16
		m    uint64
	}{
		{"zeroM", 7, 0},
		{"zeroK", 0, 64},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "bloom.bin")

			var hdr [4 + 2 + 2 + 8 + 8]byte
			copy(hdr[0:4], "SBLM")
			binary.LittleEndian.PutUint16(hdr[4:6], 1)
			binary.LittleEndian.PutUint16(hdr[6:8], tc.k)
			binary.LittleEndian.PutUint64(hdr[8:16], tc.m)
			binary.LittleEndian.PutUint64(hdr[16:24], 0) // bitsLen 0 passes the loose check
			if err := os.WriteFile(path, hdr[:], 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := reputation.LoadOrCreateBloom(path); err == nil {
				t.Errorf("LoadOrCreateBloom with k=%d m=%d should error, not panic", tc.k, tc.m)
			}
		})
	}
}

// TestLoadOrCreateBloomTruncatedHeader covers the io.ReadFull
// error branch — a file shorter than the fixed header is not a
// valid bloom filter.
func TestLoadOrCreateBloomTruncatedHeader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bloom.bin")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reputation.LoadOrCreateBloom(path); err == nil {
		t.Error("LoadOrCreateBloom on a truncated file should error")
	}
}
