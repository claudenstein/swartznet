// Package scenarios holds timing-sensitive multi-client transfer tests.
// They move real pieces between two anacrolix clients on loopback — exactly
// the shape that flakes on shared CI runners — so CI excludes this package
// and the pre-slice-done local `go test -race ./...` runs them.
package scenarios

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/wirecompat"
)

// TestMagnetTransferBetweenTwoEngines is THE Slice 2 DoD scenario: engine A
// seeds a fixture, engine B adds the magnet, completes over loopback, and
// the payload round-trips byte-identically.
func TestMagnetTransferBetweenTwoEngines(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-client transfer; run without -short")
	}
	c := wirecompat.NewCluster(t, 2)
	seed, leech := c.Nodes[0], c.Nodes[1]

	contentDir := filepath.Join(seed.DataDir, "content")
	mi, payload := wirecompat.BuildFixture(t, contentDir, "fixture.bin", 96*1024)

	hSeed, err := seed.Eng.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(contentDir, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	// The seeder MUST be verified before the leech wires up, or the leech
	// can receive an empty bitfield and stall on anacrolix's lazy verify.
	waitFor(t, 10*time.Second, "seeder verified", func() bool {
		return hSeed.T.BytesCompleted() == int64(len(payload))
	})

	// The magnet is built the real-user way, never a plumbed infohash.
	magnet := metainfo.Magnet{InfoHash: mi.HashInfoBytes(), DisplayName: "fixture.bin"}.String()
	hLeech, err := leech.Eng.AddMagnet(magnet)
	if err != nil {
		t.Fatal(err)
	}

	// DHT is off: wire the peers explicitly, both directions.
	ih := mi.HashInfoBytes()
	if _, err := leech.Eng.AddTrustedPeerEngine(ih, seed.Eng); err != nil {
		t.Fatal(err)
	}

	select {
	case <-hLeech.T.GotInfo():
	case <-time.After(10 * time.Second):
		t.Fatal("leech never fetched metadata over BEP-9")
	}

	// No DownloadAll: the engine's per-file flip is the activation path —
	// assert it actually ran.
	waitFor(t, 5*time.Second, "leech files at normal priority", func() bool {
		files, err := leech.Eng.TorrentFiles(ih.HexString())
		if err != nil || len(files) == 0 {
			return false
		}
		for _, f := range files {
			if f.Priority != "normal" {
				return false
			}
		}
		return true
	})

	waitFor(t, 15*time.Second, "leech to complete", func() bool {
		return hLeech.T.BytesCompleted() == int64(len(payload))
	})

	got, err := os.ReadFile(filepath.Join(leech.DataDir, "fixture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes", len(got))
	}

	// The seeder's snapshot reads "seeding"; the leech flips there too once
	// complete.
	waitFor(t, 5*time.Second, "leech seeding", func() bool {
		for _, s := range leech.Eng.TorrentSnapshots() {
			if s.InfoHash == ih.HexString() && s.Status == "seeding" {
				return true
			}
		}
		return false
	})
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
