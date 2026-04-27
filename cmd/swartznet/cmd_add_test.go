package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestCmdAddBadFlag covers cmdAdd's `fs.Parse` err arm.
func TestCmdAddBadFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdAdd([]string{"--no-such-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("bad-flag exit = %d, want exitUsage", code)
	}
}

// TestCmdAddMissingPositional covers `if fs.NArg() != 1` arm.
func TestCmdAddMissingPositional(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := cmdAdd(nil, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("no-args exit = %d, want exitUsage", code)
	}
}

// newAddTestEngine spins up a minimal engine with no DHT / no
// reputation / no bloom for addTorrent and printInfo helper tests.
func newAddTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.ListenPort = 0
	cfg.DisableDHT = true
	cfg.NoUpload = true
	cfg.IdentityPath = ""
	cfg.ReputationPath = ""
	cfg.SeedListPath = ""
	cfg.BloomPath = ""
	cfg.TrustPath = ""

	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

// TestAddTorrentMagnet covers addTorrent's
// `if strings.HasPrefix(target, "magnet:") { return eng.AddMagnet(target) }`
// arm. A magnet URI dispatches to AddMagnet.
func TestAddTorrentMagnet(t *testing.T) {
	t.Parallel()
	eng := newAddTestEngine(t)

	var ih [20]byte
	if _, err := rand.Read(ih[:]); err != nil {
		t.Fatal(err)
	}
	uri := "magnet:?xt=urn:btih:" + hex.EncodeToString(ih[:])
	h, err := addTorrent(eng, uri)
	if err != nil {
		t.Fatalf("addTorrent magnet: %v", err)
	}
	if h == nil {
		t.Fatal("addTorrent magnet returned nil handle")
	}
}

// TestAddTorrentFromFile covers addTorrent's else arm — non-magnet
// path goes to AddTorrentFile.
func TestAddTorrentFromFile(t *testing.T) {
	t.Parallel()
	eng := newAddTestEngine(t)

	// Create a real .torrent file via CreateTorrentFile so AddTorrentFile
	// has something legitimate to load.
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("addTorrent file path test"), 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(dir, "test.torrent")
	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: src}, torrentPath); err != nil {
		t.Fatal(err)
	}

	h, err := addTorrent(eng, torrentPath)
	if err != nil {
		t.Fatalf("addTorrent file: %v", err)
	}
	if h == nil {
		t.Fatal("addTorrent file returned nil handle")
	}
}

// TestPrintInfoSmokeTest covers printInfo on a real handle —
// ensures the formatter walks all the paths (Name/Hash/Size/
// Pieces/Files lines).
func TestPrintInfoSmokeTest(t *testing.T) {
	t.Parallel()
	eng := newAddTestEngine(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("printInfo smoke test"), 0o644); err != nil {
		t.Fatal(err)
	}
	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: src})
	if err != nil {
		t.Fatal(err)
	}
	hAny, err := eng.AddTorrentMetaInfo(mi)
	if err != nil {
		t.Fatal(err)
	}
	h := hAny.(*engine.Handle)

	var buf bytes.Buffer
	printInfo(&buf, h)
	out := buf.String()
	for _, want := range []string{"Name:", "Hash:", "Size:", "Pieces:", "Files:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in printInfo output:\n%s", want, out)
		}
	}
}
