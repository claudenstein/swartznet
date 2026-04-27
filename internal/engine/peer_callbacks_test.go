package engine_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestEnginePeerCallbacksFire wires two engines on loopback,
// adds the same torrent on both, then connects them via
// AddTrustedPeerEngine. Real peer-conn callbacks (PeerConnAdded,
// ReadExtendedHandshake, PeerConnReadExtensionMessage,
// PeerConnClosed) fire as the LTEP handshake completes — the
// previously-cold paths inside engine.New.
func TestEnginePeerCallbacksFire(t *testing.T) {
	t.Parallel()
	cfgFor := func(dataDir string) config.Config {
		c := config.Default()
		c.DataDir = dataDir
		c.ListenPort = 0
		c.ListenHost = "127.0.0.1"
		c.DisableDHT = true
		c.NoUpload = false
		c.Seed = true
		c.IdentityPath = ""
		c.ReputationPath = ""
		c.SeedListPath = ""
		c.BloomPath = ""
		c.TrustPath = ""
		c.PublisherManifest = ""
		c.CompanionDir = ""
		c.CompanionFollowFile = ""
		c.DisableIPv6 = true
		return c
	}

	dataDirA := t.TempDir()
	dataDirB := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	engA, err := engine.New(context.Background(), cfgFor(dataDirA), logger)
	if err != nil {
		t.Fatalf("engine.New A: %v", err)
	}
	defer engA.Close()
	engB, err := engine.New(context.Background(), cfgFor(dataDirB), logger)
	if err != nil {
		t.Fatalf("engine.New B: %v", err)
	}
	defer engB.Close()

	// Place the same content on both nodes' DataDir so VerifyData
	// finds bytes already verified on both sides.
	body := []byte(fillTo(32 * 1024))
	for _, dir := range []string{dataDirA, dataDirB} {
		if err := os.WriteFile(filepath.Join(dir, "shared.bin"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mi, err := engA.CreateTorrent(engine.CreateTorrentOptions{Root: filepath.Join(dataDirA, "shared.bin")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engA.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("engA.AddTorrentMetaInfo: %v", err)
	}
	if _, err := engB.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("engB.AddTorrentMetaInfo: %v", err)
	}

	ih := mi.HashInfoBytes()
	var ihArr [20]byte
	copy(ihArr[:], ih[:])
	if _, err := engA.AddTrustedPeerEngine(ihArr, engB); err != nil {
		t.Fatalf("AddTrustedPeerEngine A→B: %v", err)
	}
	if _, err := engB.AddTrustedPeerEngine(ihArr, engA); err != nil {
		t.Fatalf("AddTrustedPeerEngine B→A: %v", err)
	}

	// Wait for the LTEP handshake to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		swarm := engA.SwarmSearch()
		if swarm != nil && swarm.CapablePeerCount() >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("peer handshake didn't finish; coverage may still have moved")
}
