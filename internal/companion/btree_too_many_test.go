package companion

import (
	"testing"
)

// TestEncodeInteriorTooManyChildren covers EncodeInterior's
// `if len(children) > 65535 → err` arm at btree.go:259-261.
// 65536 children all with empty separators trips the cap before
// any payload encoding work runs.
func TestEncodeInteriorTooManyChildren(t *testing.T) {
	t.Parallel()
	children := make([]InteriorChild, 65536)
	for i := range children {
		children[i].ChildIndex = uint32(i)
	}
	if _, err := EncodeInterior(PageKindInterior, 0, children, 1<<20); err == nil {
		t.Error("EncodeInterior should reject > 65535 children")
	}
}

// TestEncodeLeafTooManyRecords covers EncodeLeaf's
// `if len(records) > 65535 → err` arm at btree.go:351-353.
// 65536 trivial records trip the cap.
func TestEncodeLeafTooManyRecords(t *testing.T) {
	t.Parallel()
	records := make([]Record, 65536)
	for i := range records {
		records[i].Kw = "k"
	}
	if _, err := EncodeLeaf(0, records, 1<<24); err == nil {
		t.Error("EncodeLeaf should reject > 65535 records")
	}
}
