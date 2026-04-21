package testlab_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	anacrolixtorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioVanillaClientPieceAndMetadata closes wire-compat matrix
// rows 8.1-A and 8.1-B together using a real anacrolix torrent.Client
// (no sn_search in its LTEP handshake) as the "vanilla" peer.
//
//   - 8.1-B: The vanilla client adds a magnet URI and receives the full
//     info dict from the SwartzNet seeder via BEP-9 (ut_metadata).
//   - 8.1-A: The same vanilla client completes downloading all pieces
//     from the SwartzNet seeder, and the bytes are verified.
func TestScenarioVanillaClientPieceAndMetadata(t *testing.T) {
	t.Parallel()
	t.Run("8.1-B_metadata_via_bep9", testVanillaClientMetadataViaBEP9)
	t.Run("8.1-A_piece_download_completes", testVanillaClientPieceDownload)
}

// testVanillaClientMetadataViaBEP9 asserts 8.1-B.
func testVanillaClientMetadataViaBEP9(t *testing.T) {
	t.Helper()
	seedEng, mi, _ := makeSeeder(t)
	ih := mi.HashInfoBytes()

	vanillaClient, err := anacrolixtorrent.NewClient(newVanillaConfig(t))
	if err != nil {
		t.Fatalf("vanilla client: %v", err)
	}
	t.Cleanup(func() { _ = vanillaClient.Close() })

	mag := metainfo.Magnet{InfoHash: ih, DisplayName: "vanilla-bep9-test.bin"}
	vt, err := vanillaClient.AddMagnet(mag.String())
	if err != nil {
		t.Fatalf("vanilla AddMagnet: %v", err)
	}
	vt.AddPeers([]anacrolixtorrent.PeerInfo{{
		Addr:    &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: seedEng.LocalPort()},
		Trusted: true,
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	select {
	case <-vt.GotInfo():
		gotIH := vt.InfoHash()
		if gotIH != ih {
			t.Errorf("got infohash %x, want %x", gotIH, ih)
		}
		t.Logf("8.1-B: BEP-9 metadata received for ih=%x", gotIH)
	case <-ctx.Done():
		t.Fatalf("8.1-B: vanilla client never got metadata: %v", ctx.Err())
	}
}

// testVanillaClientPieceDownload asserts 8.1-A.
func testVanillaClientPieceDownload(t *testing.T) {
	t.Helper()
	const payloadName = "vanilla-piece-test.bin"
	const payloadSize = 64 * 1024

	seedEng, mi, _ := makeSeeder(t)
	ih := mi.HashInfoBytes()
	payload := deterministicPayload(payloadSize)

	vanillaDataDir := t.TempDir()
	cfg := newVanillaConfig(t)
	cfg.DataDir = vanillaDataDir
	vanillaClient, err := anacrolixtorrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("vanilla client: %v", err)
	}
	t.Cleanup(func() { _ = vanillaClient.Close() })

	mag := metainfo.Magnet{InfoHash: ih, DisplayName: payloadName}
	vt, err := vanillaClient.AddMagnet(mag.String())
	if err != nil {
		t.Fatalf("vanilla AddMagnet: %v", err)
	}
	vt.AddPeers([]anacrolixtorrent.PeerInfo{{
		Addr:    &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: seedEng.LocalPort()},
		Trusted: true,
	}})

	infoCtx, infoCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer infoCancel()
	select {
	case <-vt.GotInfo():
	case <-infoCtx.Done():
		t.Fatalf("8.1-A: never got info: %v", infoCtx.Err())
	}
	vt.DownloadAll()

	completeCtx, completeCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer completeCancel()
	select {
	case <-vt.Complete().On():
	case <-completeCtx.Done():
		t.Fatalf("8.1-A: never completed: BytesCompleted=%d BytesMissing=%d: %v",
			vt.BytesCompleted(), vt.BytesMissing(), completeCtx.Err())
	}

	got, err := os.ReadFile(filepath.Join(vanillaDataDir, payloadName))
	if err != nil {
		t.Fatalf("8.1-A: read downloaded file: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("8.1-A: payload mismatch: sha256 got=%x want=%x",
			sha256.Sum256(got), sha256.Sum256(payload))
	}
	t.Logf("8.1-A: downloaded %d bytes ok, sha256=%x", len(got), sha256.Sum256(got))
}

// newVanillaConfig returns an anacrolix torrent.ClientConfig with no
// SwartzNet extensions. The client's LTEP handshake contains only the
// standard extensions (ut_metadata, ut_pex) — never sn_search.
func newVanillaConfig(t *testing.T) *anacrolixtorrent.ClientConfig {
	t.Helper()
	cfg := anacrolixtorrent.NewDefaultClientConfig()
	cfg.DataDir = t.TempDir()
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.Seed = false
	cfg.NoUpload = true
	cfg.ListenHost = anacrolixtorrent.LoopbackListenHost
	cfg.ListenPort = 0
	return cfg
}

// makeSeeder builds a small synthetic payload, creates a .torrent,
// adds it to a SwartzNet engine node, and returns the engine +
// metainfo. Cleanup is handled by t.Cleanup in NewCluster.
func makeSeeder(t *testing.T) (*engine.Engine, *metainfo.MetaInfo, func()) {
	t.Helper()
	c := testlab.NewCluster(t, 1)
	seed := c.Nodes[0]

	const payloadName = "vanilla-piece-test.bin"
	const payloadSize = 64 * 1024

	payloadPath := filepath.Join(seed.DataDir, payloadName)
	if err := os.MkdirAll(seed.DataDir, 0o755); err != nil {
		t.Fatalf("mkdir seed DataDir: %v", err)
	}
	if err := os.WriteFile(payloadPath, deterministicPayload(payloadSize), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	torrentPath := filepath.Join(t.TempDir(), "seed.torrent")
	_, mi, err := seed.Eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:        payloadPath,
		PieceLength: 16 * 1024,
		Comment:     "testlab vanilla wire-compat",
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}

	seedHandle, err := seed.Eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := seedHandle.T.VerifyDataContext(verifyCtx); err != nil {
		t.Logf("warning: VerifyDataContext: %v", err)
	}
	verifyCancel()

	if bc := seedHandle.T.BytesCompleted(); bc != int64(payloadSize) {
		t.Fatalf("seeder BytesCompleted = %d after verify, want %d", bc, payloadSize)
	}
	return seed.Eng, mi, func() {}
}
