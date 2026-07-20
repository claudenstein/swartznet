package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/indexer"
)

func seedEngine(t *testing.T, rescan time.Duration) (*Engine, string) {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "data")
	cfg := config.Config{
		DataDir:               dataDir,
		ListenPort:            0,
		DisableDHT:            true,
		DisablePortForwarding: true,
		Seed:                  true,
		IndexRescanInterval:   rescan,
	}
	e, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e, dataDir
}

func buildTextTorrent(t *testing.T, dir, name string, body []byte) *metainfo.MetaInfo {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 32 * 1024}
	if err := info.BuildFromFilePath(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	return &metainfo.MetaInfo{InfoBytes: bencode.MustMarshal(info)}
}

// TestRescanRecoversDroppedFile pins the hourly-rescan recovery (rebuild-only
// — the legacy dropped file-complete events with no recovery): a file whose
// live completion event never reached the pipeline (here, the index attaches
// only AFTER completion) is picked up by a rescan tick.
func TestRescanRecoversDroppedFile(t *testing.T) {
	// A short per-engine rescan cadence (config field, fixed at
	// construction — no shared global to race).
	e, dataDir := seedEngine(t, 50*time.Millisecond)
	content := filepath.Join(dataDir, "content")
	body := []byte(strings.Repeat("recovery corpus text. ", 300) + " rescanmarkerword end.")
	mi := buildTextTorrent(t, content, "late.txt", body)

	h, err := e.AddTorrentMetaInfoSeedFrom(mi, filepath.Join(content, "late.txt"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for h.T.BytesCompleted() != int64(len(body)) {
		if time.Now().After(deadline) {
			t.Fatal("torrent never completed")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Attach the index only now — the live submit already fired against a
	// nil pipeline and dropped. Only the rescan can recover the file.
	idx, err := indexer.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	e.SetIndex(idx)

	deadline = time.Now().Add(15 * time.Second)
	for {
		resp, err := idx.Search(indexer.SearchRequest{Query: "rescanmarkerword", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Total > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rescan never recovered the dropped file")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
