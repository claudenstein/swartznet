package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestMakeLocalHitCardArms covers makeLocalHitCard at
// search.go:253-283. Card construction reaches every
// branch: empty/non-empty Name, content vs non-content
// DocType, non-zero size, signed-by present. Callbacks
// reference st.confirmHit / st.flagHit but aren't fired
// here, so a daemon-less searchTab is sufficient.
func TestMakeLocalHitCardArms(t *testing.T) {
	t.Parallel()
	st := &searchTab{}

	// Empty Name → title falls back to InfoHash.
	st.makeLocalHitCard(indexer.SearchHit{
		Name:     "",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		DocType:  "torrent",
		Score:    0.5,
	})

	// content DocType + size + signature → exercises every
	// subtitle-extension arm.
	st.makeLocalHitCard(indexer.SearchHit{
		Name:      "ubuntu",
		InfoHash:  "0123456789abcdef0123456789abcdef01234567",
		DocType:   "content",
		FilePath:  "/path/to/file.iso",
		SizeBytes: 4500000000,
		Score:     0.9,
		SignedBy:  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	})
}

// TestMakeSwarmHitCardArms covers makeSwarmHitCard at
// search.go:285-305 — empty/non-empty Name, non-zero Size.
func TestMakeSwarmHitCardArms(t *testing.T) {
	t.Parallel()
	st := &searchTab{}

	st.makeSwarmHitCard(swarmsearch.MergedHit{
		Name:     "",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Score:    100,
		Seeders:  5,
	})
	st.makeSwarmHitCard(swarmsearch.MergedHit{
		Name:     "alpine",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Score:    50,
		Seeders:  3,
		Sources:  []string{"1.2.3.4:6881"},
		Size:     1024 * 1024 * 10,
	})
}

// TestMakeDHTHitCardArms covers makeDHTHitCard at
// search.go:307-330 — empty/non-empty Name, non-zero Size,
// BloomHit true/false.
func TestMakeDHTHitCardArms(t *testing.T) {
	t.Parallel()
	st := &searchTab{}

	st.makeDHTHitCard(dhtindex.LookupHit{
		Name:     "",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Score:    0.7,
	})
	st.makeDHTHitCard(dhtindex.LookupHit{
		Name:     "fedora",
		InfoHash: "0123456789abcdef0123456789abcdef01234567",
		Score:    0.9,
		Seeders:  10,
		Sources:  []string{"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"},
		Size:     2 * 1024 * 1024 * 1024,
		BloomHit: true,
	})
}
