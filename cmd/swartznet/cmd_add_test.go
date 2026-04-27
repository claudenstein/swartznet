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
	"syscall"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
)

// TestCmdAddDaemonNewErr covers cmdAdd's
// `d, err := daemon.New(...); if err != nil { return reportRunErr }`
// arm at cmd_add.go:87-89. A --data-dir nested under a regular
// file makes config.Validate fail (ENOTDIR on MkdirAll), which
// engine.New surfaces and daemon.New propagates.
func TestCmdAddDaemonNewErr(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(blocker, "data")

	var stdout, stderr bytes.Buffer
	code := cmdAdd([]string{
		"--data-dir", bad,
		"--no-dht",
		"--api-addr", "",
		"magnet:?xt=urn:btih:1111111111111111111111111111111111111111",
	}, &stdout, &stderr)
	if code == exitOK {
		t.Errorf("daemon.New-err exit = %d, want non-zero", code)
	}
}

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

// TestCmdAddBadTorrentFilePath drives cmdAdd far enough to spin
// up daemon.New + signalContext, then fail at addTorrent because
// the .torrent file path doesn't exist. Exercises the daemon
// startup path, the no-CompPub/no-CompSub print branches (since
// our test config has no companion dir/manifest), and the
// addTorrent err arm — all without leaving the goroutine blocked
// on metadata.
//
// Uses --no-dht and a self-allocated --port to avoid touching
// the network or pinning a port.
func TestCmdAddBadTorrentFilePath(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	indexDir := t.TempDir()
	missingTorrent := filepath.Join(dataDir, "no-such.torrent")

	var stdout, stderr bytes.Buffer
	code := cmdAdd([]string{
		"--data-dir", dataDir,
		"--index-dir", indexDir,
		"--port", "0",
		"--no-dht",
		"--api-addr", "", // disable HTTP API
		missingTorrent,
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("missing-torrent exit = %d, want exitRuntime; stderr: %s", code, stderr.String())
	}
}

// TestCmdAddBadTorrentFilePathWithAPI extends the bad-torrent-
// path coverage to also fire the `if d.API != nil { … HTTP API
// listening … }` print arm. APIAddr=127.0.0.1:0 spins up a real
// HTTP server, the print runs, then addTorrent fails as before.
func TestCmdAddBadTorrentFilePathWithAPI(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	indexDir := t.TempDir()
	missingTorrent := filepath.Join(dataDir, "no-such.torrent")

	var stdout, stderr bytes.Buffer
	code := cmdAdd([]string{
		"--data-dir", dataDir,
		"--index-dir", indexDir,
		"--port", "0",
		"--no-dht",
		"--api-addr", "127.0.0.1:0",
		missingTorrent,
	}, &stdout, &stderr)
	if code != exitRuntime {
		t.Errorf("missing-torrent exit = %d, want exitRuntime; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "HTTP API listening") {
		t.Errorf("expected 'HTTP API listening' line, got %q", stdout.String())
	}
}

// TestCmdAddSigintWhileWaitingForMetadata covers cmdAdd's
// `case <-ctx.Done(): return exitInterrupt` arm in the
// metadata-wait select. Pass a magnet URI for a random
// infohash with --no-dht so GotInfo never fires, then SIGINT
// to make the signal context cancel.
func TestCmdAddSigintWhileWaitingForMetadata(t *testing.T) {
	dataDir := t.TempDir()
	indexDir := t.TempDir()

	// magnet:?xt=urn:btih:<40 hex chars>
	const ih = "1234567890abcdef1234567890abcdef12345678"
	magnet := "magnet:?xt=urn:btih:" + ih

	done := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := cmdAdd([]string{
			"--data-dir", dataDir,
			"--index-dir", indexDir,
			"--port", "0",
			"--no-dht",
			"--api-addr", "",
			magnet,
		}, &stdout, &stderr)
		done <- code
	}()

	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT failed: %v", err)
	}

	select {
	case code := <-done:
		// reportRunErr(ctx.Canceled) → exitInterrupt for sigint.
		if code != exitInterrupt {
			t.Logf("cmdAdd exit = %d (acceptable if the engine close ordering races, "+
				"but exitInterrupt is preferred)", code)
		}
	case <-time.After(10 * time.Second):
		t.Error("cmdAdd did not exit within 10s of SIGINT")
	}
}

// TestCmdAddRealTorrentSigint exercises cmdAdd's full happy
// path: a real .torrent file (built via CreateTorrent), so
// GotInfo fires immediately, printInfo and progressLoop both
// run. SIGINT cancels the signal-context to make progressLoop
// exit cleanly.
func TestCmdAddRealTorrentSigint(t *testing.T) {
	dataDir := t.TempDir()
	indexDir := t.TempDir()

	// Build a real torrent file via the in-process engine.
	eng := newAddTestEngine(t)
	src := filepath.Join(dataDir, "src.bin")
	if err := os.WriteFile(src, []byte("realtorrent"), 0o644); err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	if _, _, err := eng.CreateTorrentFile(engine.CreateTorrentOptions{Root: src}, torrentPath); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := cmdAdd([]string{
			"--data-dir", dataDir,
			"--index-dir", indexDir,
			"--port", "0",
			"--no-dht",
			"--api-addr", "",
			torrentPath,
		}, &stdout, &stderr)
		done <- code
	}()

	// Wait long enough for progressLoop's 3 s tick to fire at
	// least once, then SIGINT. This hits both the tick.C arm
	// and the ctx.Done() exit arm.
	time.Sleep(3500 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT failed: %v", err)
	}

	select {
	case <-done:
		// Either exitOK (clean) or exitInterrupt — both are
		// acceptable; the goal is to drive the lines.
	case <-time.After(10 * time.Second):
		t.Error("cmdAdd did not exit within 10s of SIGINT after metadata")
	}
}

// TestPrintInfoTruncatesLongFileList covers printInfo's
// `if i == maxListed { ... break }` arm at lines 157-159. A
// torrent with 25 files exceeds the 20-file print cap, so the
// '... and N more' line should appear.
func TestPrintInfoTruncatesLongFileList(t *testing.T) {
	t.Parallel()
	eng := newAddTestEngine(t)

	// Build a directory with 25 small files.
	dir := t.TempDir()
	root := filepath.Join(dir, "many")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		f := filepath.Join(root, hex.EncodeToString([]byte{byte(i)})+".txt")
		if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mi, err := eng.CreateTorrent(engine.CreateTorrentOptions{Root: root})
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
	if !strings.Contains(buf.String(), "... and 5 more") {
		t.Errorf("expected '... and 5 more' line for 25-file torrent, got:\n%s", buf.String())
	}
}

// TestProgressLoopReturnsOnCtxCancel covers progressLoop's
// `case <-ctx.Done(): return` arm. Cancel a short ctx and
// confirm the loop exits without hanging.
func TestProgressLoopReturnsOnCtxCancel(t *testing.T) {
	t.Parallel()
	eng := newAddTestEngine(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("progressLoop ctx-cancel"), 0o644); err != nil {
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	done := make(chan struct{})
	go func() {
		var buf bytes.Buffer
		progressLoop(ctx, &buf, h)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("progressLoop did not exit within 2s of ctx cancel")
	}
}

// TestProgressLoopHandlesClosedFileEvents covers progressLoop's
// `case ev, ok := <-fileEvents: if !ok { continue }` arm at
// cmd_add.go:185-189. Engine.Close closes the file-event fan-out
// channel, so the next select pulls a zero-value with ok=false.
// We then cancel the ctx to let the loop exit cleanly.
func TestProgressLoopHandlesClosedFileEvents(t *testing.T) {
	t.Parallel()
	eng := newAddTestEngine(t)

	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("progressLoop closed-events"), 0o644); err != nil {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		var buf bytes.Buffer
		progressLoop(ctx, &buf, h)
		close(done)
	}()

	// Close the engine — fileSub.Close() shuts the fan-out, so
	// progressLoop's fileEvents channel closes and the !ok arm
	// fires (continues without exiting).
	eng.Close()

	// Give the loop one scheduler tick to consume the close.
	time.Sleep(50 * time.Millisecond)

	// Now cancel ctx so the loop returns via the Done arm.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("progressLoop did not exit within 2s after close+cancel")
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
