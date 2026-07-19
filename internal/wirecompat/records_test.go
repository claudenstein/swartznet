package wirecompat

import (
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/contracts/token"
)

// TestEngineMintsAggregateRecords: a signing engine mints one signed record per
// torrent name-keyword on GotInfo, populating its record cache — even with no
// Layer-L index wired.
func TestEngineMintsAggregateRecords(t *testing.T) {
	c := NewCluster(t, 1)
	eng := c.Nodes[0].Eng

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var pk [32]byte
	copy(pk[:], pub)
	eng.SetSigner(priv, pk)

	content := filepath.Join(c.Nodes[0].DataDir, "content")
	// A multi-word name yields multiple keyword records.
	mi, _ := BuildFixtureBytes(t, content, "debian bookworm netinst amd64.iso", []byte("payload bytes here for the torrent"))
	if _, err := eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(content, "debian bookworm netinst amd64.iso")); err != nil {
		t.Fatal(err)
	}

	want := len(token.TokenizeAll("debian bookworm netinst amd64.iso")) // 4
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if eng.RecordCache().Len() >= want {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	n := eng.RecordCache().Len()
	if n != want {
		t.Fatalf("minted %d records, want %d (one per name-keyword)", n, want)
	}
	// Every minted record verifies and carries our pubkey.
	for _, r := range eng.RecordCache().Snapshot() {
		if r.Pk != pk {
			t.Errorf("record signed by the wrong key")
		}
		if err := r.Verify(); err != nil {
			t.Errorf("minted record fails verification: %v", err)
		}
	}
	t.Logf("minted %d keyword records", n)
}

// TestEngineNoSignerNoRecords: without a signer, no records are minted.
func TestEngineNoSignerNoRecords(t *testing.T) {
	c := NewCluster(t, 1)
	eng := c.Nodes[0].Eng // no SetSigner
	content := filepath.Join(c.Nodes[0].DataDir, "content")
	mi, _ := BuildFixtureBytes(t, content, "ubuntu.iso", []byte("payload"))
	if _, err := eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(content, "ubuntu.iso")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if eng.RecordCache().Len() != 0 {
		t.Errorf("minted %d records without a signer, want 0", eng.RecordCache().Len())
	}
}
