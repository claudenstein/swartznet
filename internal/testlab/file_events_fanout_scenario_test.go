package testlab_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioFileEventsFanOutPipelineCoverage pins the contract that
// every file that reaches the leech is also delivered to every Handle
// subscriber *independently*, even when one subscriber is the ingest
// pipeline and another is an unrelated consumer (the CLI progressLoop
// in the shipped code).
//
// Before the fan-out refactor the tracker exposed a single buffered
// channel; two goroutines reading from it split events between
// themselves, so the ingest pipeline silently dropped whichever
// events the other reader happened to grab. In a 15-file torrent
// that meant IndexedFiles stalled at 11–13/15 with no warning.
//
// The test constructs a seed → leech pair with a 12-file torrent,
// attaches a second goroutine that drains Handle.SubscribeFileEvents
// just like the CLI does, and then asserts:
//   1. The ingest pipeline processed *every* file (IndexedFiles == Files).
//   2. The side consumer also saw every file.
func TestScenarioFileEventsFanOutPipelineCoverage(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	seed := c.Nodes[0]
	leech := c.Nodes[1]

	const rootName = "fanout-root"
	const fileCount = 12
	root := filepath.Join(seed.DataDir, rootName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	for i := 0; i < fileCount; i++ {
		body := []byte(fmt.Sprintf("fanout file %02d marker=azure-%02d-content\n", i, i))
		// Pad so each file is larger than a single piece, which gives
		// the tracker multiple piece-state changes per file to emit
		// completion for. The 16 KiB piece length in the create call
		// below means a 20 KiB body spans two pieces.
		padded := make([]byte, 20*1024)
		copy(padded, body)
		p := filepath.Join(root, fmt.Sprintf("file-%02d.txt", i))
		if err := os.WriteFile(p, padded, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	torrentPath := filepath.Join(t.TempDir(), "fanout.torrent")
	_, mi, err := seed.Eng.CreateTorrentFile(engine.CreateTorrentOptions{
		Root:        root,
		PieceLength: 16 * 1024,
	}, torrentPath)
	if err != nil {
		t.Fatalf("CreateTorrentFile: %v", err)
	}
	seedHandle, err := seed.Eng.AddTorrentFile(torrentPath)
	if err != nil {
		t.Fatalf("seed.AddTorrentFile: %v", err)
	}

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := seedHandle.T.VerifyDataContext(verifyCtx); err != nil {
		t.Logf("warning: VerifyDataContext: %v", err)
	}
	verifyCancel()

	ih := mi.HashInfoBytes()
	magnetURI := metainfo.Magnet{InfoHash: ih, DisplayName: rootName}.String()
	leechHandle, err := leech.Eng.AddMagnet(magnetURI)
	if err != nil {
		t.Fatalf("leech.AddMagnet: %v", err)
	}

	// Attach the "noisy" second subscriber BEFORE the transfer starts,
	// mirroring the CLI where progressLoop subscribes right after
	// AddMagnet returns. This is the exact race the old code lost.
	var sideSeen atomic.Int64
	sideCh := leechHandle.SubscribeFileEvents()
	sideDone := make(chan struct{})
	go func() {
		defer close(sideDone)
		for range sideCh {
			sideSeen.Add(1)
		}
	}()

	if _, err := leech.Eng.AddTrustedPeerEngine(ih, seed.Eng); err != nil {
		t.Fatalf("leech.AddTrustedPeerEngine: %v", err)
	}

	gotInfoCtx, cancelGotInfo := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelGotInfo()
	select {
	case <-leechHandle.T.GotInfo():
	case <-gotInfoCtx.Done():
		c.DumpLogs(t)
		t.Fatalf("leech never got metadata: %v", gotInfoCtx.Err())
	}
	leechHandle.T.DownloadAll()

	completeCtx, cancelComplete := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelComplete()
	select {
	case <-leechHandle.T.Complete().On():
	case <-completeCtx.Done():
		c.DumpLogs(t)
		t.Fatalf("leech never completed: BytesCompleted=%d BytesMissing=%d",
			leechHandle.T.BytesCompleted(), leechHandle.T.BytesMissing())
	}

	// Poll the snapshot until IndexedFiles reaches Files. 10 s is
	// plenty because each file is a plaintext slice of a few kilobytes.
	ihHex := ih.HexString()
	deadline := time.Now().Add(15 * time.Second)
	var last engine.TorrentSnapshot
	for time.Now().Before(deadline) {
		for _, s := range leech.Eng.TorrentSnapshots() {
			if equalFoldHexTest(s.InfoHash, ihHex) {
				last = s
				break
			}
		}
		if last.Files > 0 && last.IndexedFiles >= int64(last.Files) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if last.Files == 0 {
		c.DumpLogs(t)
		t.Fatalf("leech never produced a snapshot for %s", ihHex)
	}
	if last.IndexedFiles < int64(last.Files) {
		c.DumpLogs(t)
		t.Fatalf("pipeline stalled: IndexedFiles=%d Files=%d — some file-complete events "+
			"never reached the ingest pipeline (fan-out regression?)",
			last.IndexedFiles, last.Files)
	}

	// Wait briefly for the side consumer to drain the same set of events.
	sideDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(sideDeadline) && sideSeen.Load() < int64(last.Files) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := sideSeen.Load(); got < int64(last.Files) {
		c.DumpLogs(t)
		t.Fatalf("side consumer only saw %d/%d events — fan-out is not reaching every subscriber",
			got, last.Files)
	}
}

// equalFoldHexTest mirrors the daemon test helper; kept local so the
// testlab package stays self-contained.
func equalFoldHexTest(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
