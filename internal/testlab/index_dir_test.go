package testlab_test

import (
	"os"
	"testing"

	"github.com/swartznet/swartznet/internal/testlab"
)

// TestNodeIndexDirHoldsTheIndex guards against the dead-config
// trap where Node.IndexDir reported a path (root/bleve) that the
// node never actually opened its Bleve index in (root/bleve-idx).
// A scenario test that trusts Node.IndexDir to locate index files
// must find them there. We seed one document so the index has
// on-disk segments, then assert the reported directory exists and
// is non-empty.
//
// Before the fix the reported directory either did not exist or
// was empty, because the real index lived in a sibling directory.
func TestNodeIndexDirHoldsTheIndex(t *testing.T) {
	c := testlab.NewCluster(t, 1)
	n := c.Nodes[0]

	// Force the index to flush some on-disk state.
	n.IndexTorrent(t, 0x42, "ubuntu 24.04 desktop iso")

	if n.IndexDir == "" {
		t.Fatal("Node.IndexDir is empty")
	}
	entries, err := os.ReadDir(n.IndexDir)
	if err != nil {
		t.Fatalf("Node.IndexDir %q is not a readable directory: %v", n.IndexDir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("Node.IndexDir %q is empty; the Bleve index does not live where the node reports it", n.IndexDir)
	}
}
