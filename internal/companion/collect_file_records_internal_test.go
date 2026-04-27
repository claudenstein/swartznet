package companion

import (
	"testing"

	"github.com/swartznet/swartznet/internal/indexer"
)

// TestCollectFileRecordsFillsLatePath covers collectFileRecords'
// `if b.path == "" && c.FilePath != "" { b.path = c.FilePath }`
// arm. Sequencing matters: the FIRST ContentDoc seen for a file
// must have FilePath="", a LATER one for the same file must
// have a non-empty FilePath. Internal test so we can control
// ordering by calling collectFileRecords directly.
func TestCollectFileRecordsFillsLatePath(t *testing.T) {
	t.Parallel()
	docs := []indexer.ContentDoc{
		{InfoHash: "x", FileIndex: 0, FilePath: "", Text: "first"},
		{InfoHash: "x", FileIndex: 0, FilePath: "late.txt", Text: "second"},
	}
	out := collectFileRecords([]string{"late.txt"}, docs, DefaultBuildOptions())
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].Path != "late.txt" {
		t.Errorf("FileRecord.Path = %q, want late.txt", out[0].Path)
	}
}
