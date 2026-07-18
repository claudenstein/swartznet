package indexer_test

import (
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestPipelineSubmittedSet covers the rescan dedup guard: Submit records
// (infohash, fileIndex) so the engine's hourly rescan resubmits only
// dropped events, and ForgetSubmitted clears a torrent's entries so a
// re-add after Forget re-indexes cleanly.
func TestPipelineSubmittedSet(t *testing.T) {
	t.Parallel()
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "idx"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	p := indexer.NewPipeline(idx, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	p.Start()
	defer p.Stop()

	const ih = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"

	if p.WasSubmitted(ih, 0) {
		t.Fatal("WasSubmitted true before any Submit")
	}
	if !p.Submit(indexer.FileInput{
		InfoHash:  ih,
		FileIndex: 0,
		Path:      "a.txt",
		Size:      5,
		OpenReader: func() (io.Reader, error) {
			return strings.NewReader("hello"), nil
		},
	}) {
		t.Fatal("Submit returned false")
	}

	if !p.WasSubmitted(ih, 0) {
		t.Error("WasSubmitted(ih, 0) = false after Submit")
	}
	if !p.WasSubmitted(strings.ToUpper(ih), 0) {
		t.Error("WasSubmitted must be case-insensitive on the infohash")
	}
	if p.WasSubmitted(ih, 1) {
		t.Error("WasSubmitted(ih, 1) = true for a never-submitted file index")
	}

	p.ForgetSubmitted(ih)
	if p.WasSubmitted(ih, 0) {
		t.Error("WasSubmitted true after ForgetSubmitted")
	}
}
