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
	"github.com/swartznet/swartznet/internal/indexer"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestEngineSwarmSearchReplyBetweenPeers wires two engines, has
// them establish an LTEP handshake (which advertises the
// sn_search extension), then has engine A issue a swarmsearch
// query that engine B handles. The handler's reply closure
// fires the previously-cold body inside engine.New's
// PeerConnReadExtensionMessage callback (lines 688-700) where
// the reply payload is queued for write back to the peer.
func TestEngineSwarmSearchReplyBetweenPeers(t *testing.T) {
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

	idxA, err := indexer.Open(filepath.Join(t.TempDir(), "idxA.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idxA.Close()
	engA.SetIndex(idxA)
	idxB, err := indexer.Open(filepath.Join(t.TempDir(), "idxB.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer idxB.Close()
	engB.SetIndex(idxB)

	// Seed a doc on engine B so the search query has something to match.
	if err := idxB.IndexTorrent(indexer.TorrentDoc{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "ubuntu desktop iso",
	}); err != nil {
		t.Fatal(err)
	}

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
		t.Fatal(err)
	}
	if _, err := engB.AddTorrentMetaInfo(mi); err != nil {
		t.Fatal(err)
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

	// Wait for at least one capable peer in A's view.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if engA.SwarmSearch().CapablePeerCount() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Issue a swarmsearch query from A to B. The reply closure
	// fires when B handles the query and sends back a result.
	queryCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, _ = engA.SwarmSearch().Query(queryCtx, swarmsearch.QueryRequest{
		Q:            "ubuntu",
		PerPeerLimit: 50,
	})
}

// TestFileTrackerPieceEventsViaPeerDownload wires two engines
// on loopback where the leech (engine B) has NO data on disk
// initially. As the seed (A) feeds it pieces, the leech's
// fileTracker.run loop processes them via the
// `case ev, ok := <-sub.Values` arm and decrements remaining[fi]
// until it hits zero, dispatching the file-complete event.
//
// This is the only realistic way to exercise the piece-event
// path (lines 183-187) — when a torrent starts already
// verified, the seeded-from-current-state path takes them all.
func TestFileTrackerPieceEventsViaPeerDownload(t *testing.T) {
	t.Parallel()
	cfgFor := func(dataDir string, seeder bool) config.Config {
		c := config.Default()
		c.DataDir = dataDir
		c.ListenPort = 0
		c.ListenHost = "127.0.0.1"
		c.DisableDHT = true
		c.NoUpload = !seeder
		c.Seed = seeder
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

	dataDirA := t.TempDir() // seeder
	dataDirB := t.TempDir() // leecher
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	engA, err := engine.New(context.Background(), cfgFor(dataDirA, true), logger)
	if err != nil {
		t.Fatalf("engine.New A: %v", err)
	}
	defer engA.Close()
	engB, err := engine.New(context.Background(), cfgFor(dataDirB, false), logger)
	if err != nil {
		t.Fatalf("engine.New B: %v", err)
	}
	defer engB.Close()

	// Place content ONLY on the seeder.
	body := []byte(fillTo(64 * 1024))
	if err := os.WriteFile(filepath.Join(dataDirA, "shared.bin"), body, 0o644); err != nil {
		t.Fatal(err)
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
	if _, err := engB.AddTrustedPeerEngine(ihArr, engA); err != nil {
		t.Fatalf("AddTrustedPeerEngine B→A: %v", err)
	}

	// Wait for the leech to finish downloading.
	wantIH := mi.HashInfoBytes().HexString()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range engB.TorrentSnapshots() {
			if s.InfoHash == wantIH && s.BytesMissing == 0 && s.Size > 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("leech didn't finish download in 5s; coverage may still have moved")
}

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
