package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/engine"
)

// addCraftedInfo marshals a hand-built info dict into the engine via
// AddTorrentMetaInfo (so metadata is immediately available, exactly
// like a hostile publisher answering ut_metadata) and returns the
// infohash. No piece data ever exists on disk.
func addCraftedInfo(t *testing.T, eng *engine.Engine, info metainfo.Info) [20]byte {
	t.Helper()
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: ib}
	if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
		t.Fatalf("AddTorrentMetaInfo: %v", err)
	}
	return [20]byte(mi.HashInfoBytes())
}

// TestFetchCompanionTorrentRejectsOversized — a single-file companion
// whose info dict declares a length above the maxCompanionBytes cap
// must be rejected BEFORE any pieces are requested. The declared
// length is fully attacker-controlled (the BEP-46 pointer resolves
// to an untrusted infohash); without the cap the only bound on the
// download was the subscriber's wall-clock FetchTimeout, letting a
// fast hostile publisher fill DataDir.
func TestFetchCompanionTorrentRejectsOversized(t *testing.T) {
	t.Parallel()
	eng, cleanup := newCompanionTestEngine(t)
	t.Cleanup(cleanup)

	// 32 MiB + 1 byte across 16 MiB pieces → 3 piece hashes.
	ih := addCraftedInfo(t, eng, metainfo.Info{
		Name:        "huge-index.json.gz",
		PieceLength: 16 << 20,
		Length:      (32 << 20) + 1,
		Pieces:      make([]byte, 20*3),
	})

	// The guard fires straight after GotInfo (immediate — metadata is
	// local), well before the download poll loop, so a short timeout
	// proves the call did not start waiting on data.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := eng.FetchCompanionTorrent(ctx, ih)
	if err == nil {
		t.Fatal("FetchCompanionTorrent should reject an oversized companion torrent")
	}
	if !strings.Contains(err.Error(), "exceeds cap") {
		t.Errorf("err = %v, want 'exceeds cap' message", err)
	}
}
