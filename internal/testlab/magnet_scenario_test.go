package testlab_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioMagnetDownload is the headline magnet-flow scenario:
// one node seeds a real .torrent over peer-wire, a second node adds
// it via magnet URI, and the leech receives every byte plus
// metadata. No DHT, no tracker — peers are wired manually over
// loopback, which keeps the test hermetic and fast.
//
// Why this is load-bearing:
//  - It is the only test in the project that drives the full
//    metainfo-from-magnet fetch path end-to-end. Existing multi-
//    node tests (companion_scenario_test.go) pre-share the infohash
//    and let the subscriber call AddInfoHash directly, so they
//    never exercise magnet parsing + DHT-free metainfo exchange.
//  - It guards the CLI's real user flow: `swartznet add <magnet>`.
//    Without this test, a regression in client.AddMagnet or the
//    LTEP metadata-fetch extension would only surface when a human
//    ran the binary.
//
// Runtime budget: < 5s on a modern laptop. The payload is ~96 KiB
// across 4 pieces so both hashing and transfer are near-instant.
func TestScenarioMagnetDownload(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	seed := c.Nodes[0]
	leech := c.Nodes[1]

	// ------------------------------------------------------------
	// 1. Produce a deterministic payload and lay it in the seeder's
	//    DataDir. anacrolix's file-storage layer reads pieces from
	//    DataDir+info.Name so the payload must live there — writing
	//    anywhere else leaves the seeder with unverifiable bytes
	//    and the leech download stalls.
	const payloadName = "magnet-scenario.bin"
	const payloadSize = 96 * 1024
	payload := deterministicPayload(payloadSize)
	payloadPath := filepath.Join(seed.DataDir, payloadName)
	if err := os.MkdirAll(seed.DataDir, 0o755); err != nil {
		t.Fatalf("mkdir seed DataDir: %v", err)
	}
	if err := os.WriteFile(payloadPath, payload, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	// ------------------------------------------------------------
	// 2. Create a .torrent that targets the payload on disk and
	//    add it to the seeder. Small pieces (32 KiB) so a ~96 KiB
	//    payload yields three pieces — enough to exercise the
	//    piece-request loop without inflating test runtime.
	torrentPath := filepath.Join(t.TempDir(), "seed.torrent")
	_, mi, err := seed.Eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:        payloadPath,
		PieceLength: 32 * 1024,
		Comment:     "testlab magnet scenario",
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}

	seedHandle, err := seed.Eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("seed.AddTorrentFile: %v", err)
	}

	// Force the seeder to hash its local copy so pieces show as
	// complete before the leech connects. Without this the leech
	// may open a peer-wire session, receive an empty bitfield, and
	// stall until anacrolix's own lazy verification runs — which
	// is timing-dependent and flaky on loaded CI.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := seedHandle.T.VerifyDataContext(verifyCtx); err != nil {
		t.Logf("warning: VerifyDataContext: %v", err)
	}
	verifyCancel()
	if bc := seedHandle.T.BytesCompleted(); bc != int64(payloadSize) {
		t.Fatalf("seeder BytesCompleted = %d after verify, want %d", bc, payloadSize)
	}

	// ------------------------------------------------------------
	// 3. Compute the magnet URI from the metainfo and hand it to
	//    the leech. This is the real user flow — no infohash is
	//    plumbed directly; the leech parses it out of the URI the
	//    same way the CLI does.
	ih := mi.HashInfoBytes()
	mag := metainfo.Magnet{
		InfoHash:    ih,
		DisplayName: payloadName,
	}
	magnetURI := mag.String()

	leechHandle, err := leech.Eng.AddMagnet(magnetURI)
	if err != nil {
		t.Fatalf("leech.AddMagnet: %v", err)
	}

	// Pre-wire the seeder as a trusted peer of the leech. The
	// leech's engine has no DHT and no tracker, so without this it
	// cannot find anyone to fetch metadata from.
	nAdded, err := leech.Eng.AddTrustedPeerEngine(ih, seed.Eng)
	if err != nil {
		t.Fatalf("leech.AddTrustedPeerEngine: %v", err)
	}
	if nAdded == 0 {
		t.Fatal("leech.AddTrustedPeerEngine: 0 peers added")
	}

	// ------------------------------------------------------------
	// 4. Wait for metadata (GotInfo) then for every piece to land.
	//    The magnet-flow asserts two separate pieces of protocol
	//    work end-to-end: BEP-9 metadata exchange followed by BEP-3
	//    piece transfer.
	gotInfoCtx, cancelGotInfo := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelGotInfo()
	select {
	case <-leechHandle.T.GotInfo():
	case <-gotInfoCtx.Done():
		c.DumpLogs(t)
		t.Fatalf("leech never got metadata: %v", gotInfoCtx.Err())
	}

	// After metadata arrives anacrolix still needs to actually
	// request pieces. The engine defaults to "download everything"
	// but make it explicit so a future priority-default change
	// doesn't silently break this test.
	leechHandle.T.DownloadAll()

	completeCtx, cancelComplete := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelComplete()
	select {
	case <-leechHandle.T.Complete().On():
	case <-completeCtx.Done():
		c.DumpLogs(t)
		t.Fatalf("leech never completed: BytesCompleted=%d BytesMissing=%d err=%v",
			leechHandle.T.BytesCompleted(), leechHandle.T.BytesMissing(), completeCtx.Err())
	}

	// ------------------------------------------------------------
	// 5. Assert the leech's on-disk copy matches the seeder's
	//    payload byte-for-byte. anacrolix has already verified
	//    every piece against the metainfo's hashes so this is a
	//    belt-and-suspenders check against file-storage layout
	//    bugs (e.g. wrong DataDir, wrong filename).
	leechPath := filepath.Join(leech.DataDir, payloadName)
	got, err := os.ReadFile(leechPath)
	if err != nil {
		t.Fatalf("read leech payload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("leech payload mismatch: got %d bytes (sha256=%x), want %d bytes (sha256=%x)",
			len(got), sha256.Sum256(got), len(payload), sha256.Sum256(payload))
	}
}

// deterministicPayload returns n bytes of pseudo-random data
// derived from a fixed seed. Tests need reproducibility (so a
// failure dump is comparable across runs) but also enough entropy
// that anacrolix's piece hashes aren't degenerate.
func deterministicPayload(n int) []byte {
	out := make([]byte, n)
	// Simple linear-feedback generator — good enough to make
	// every piece distinct without pulling in crypto/rand. The
	// constants are arbitrary.
	var s uint32 = 0xdeadbeef
	for i := range out {
		s = s*1664525 + 1013904223
		out[i] = byte(s >> 16)
	}
	return out
}
