package daemon_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/indexer"
)

// TestCompanionPublisherWritesToCompanionDir is a regression guard
// for the bug where daemon.New set the companion publisher's output
// directory to Cfg.DataDir instead of Cfg.CompanionDir. CompanionDir
// must be the storage location, not merely an enable flag — a default
// install otherwise scatters companion artifacts through the content
// tree.
//
// Uses the in-process regtest DHT so the engine exposes a real
// PointerPutter + identity (both required for the companion publisher
// to start) without touching the mainline network.
func TestCompanionPublisherWritesToCompanionDir(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	companionDir := filepath.Join(dir, "companion")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.IndexDir = t.TempDir()
	cfg.CompanionDir = companionDir
	cfg.CompanionFollowFile = "" // skip subscriber follow-file load
	cfg.ListenPort = 0
	cfg.DisableDHT = false
	cfg.NoUpload = true
	cfg.IdentityPath = filepath.Join(dir, "identity.key")
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""
	cfg.Regtest = true // in-process DHT, no mainline traffic

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := daemon.New(context.Background(), daemon.Options{Cfg: cfg, Log: log})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	defer d.Close()

	if d.CompPub == nil {
		t.Fatal("companion publisher should be wired when CompanionDir + DHT + identity are present")
	}

	// The publisher only writes artifacts when the local index has at
	// least one torrent. Seed one so refreshOnce produces output.
	if err := d.Index.IndexTorrent(indexer.TorrentDoc{
		InfoHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:      "companion-dir-test",
		FilePaths: []string{"file.txt"},
		SizeBytes: 1234,
		FileCount: 1,
		AddedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("IndexTorrent: %v", err)
	}

	// Force publish cycles until the worker writes (the initial
	// auto-refresh on Start may have raced ahead of our IndexTorrent).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = d.CompPub.RefreshNow() // ErrTooSoon is fine; just keep nudging
		if len(dirEntries(t, companionDir)) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	companionFiles := dirEntries(t, companionDir)
	if len(companionFiles) == 0 {
		t.Fatalf("expected companion artifacts under CompanionDir %q, found none", companionDir)
	}

	// The content DataDir must NOT have received companion artifacts.
	for _, name := range dirEntries(t, dataDir) {
		if name == "companion.torrent" ||
			filepath.Ext(name) == ".gz" ||
			strings.HasPrefix(name, "swartznet-content-index") {
			t.Errorf("companion artifact %q leaked into DataDir %q", name, dataDir)
		}
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read dir %q: %v", dir, err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
