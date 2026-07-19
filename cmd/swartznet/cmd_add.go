package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/engine"
)

// cmdAdd adds a torrent and runs the node — in SwartzNet, add IS the daemon:
// the process serves (and seeds) until a signal arrives, and a clean Ctrl-C
// exits 130, the normal end of a session.
func cmdAdd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, cancel := signalContext(context.Background())
	defer cancel()
	return addWithContext(ctx, args, stdin, stdout, stderr)
}

// addWithContext is cmdAdd minus signal wiring, so tests drive the full
// lifecycle with their own cancellable context.
func addWithContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "data directory for downloaded content")
	indexDir := fs.String("index-dir", "", "search index directory (default: XDG data dir)")
	port := fs.Int("port", -1, "listen port (0 = OS-assigned)")
	apiAddr := fs.String("api-addr", "localhost:7654", "HTTP API listen address (empty to disable)")
	identityPath := fs.String("identity", "", "path to the ed25519 identity.key file (load-only unless it is the default path, which is auto-created)")
	noDHT := fs.Bool("no-dht", false, "disable the mainline DHT entirely")
	noDHTPublish := fs.Bool("no-dht-publish", false, "stay on the DHT but suppress Layer-D (BEP-44) keyword publishing under your identity")
	leechOnly := fs.Bool("leech-only", false, "disable uploading (debug)")
	noIndex := fs.Bool("no-index", false, "don't index downloaded content at all")
	var dhtBootstrap stringSliceFlag
	fs.Var(&dhtBootstrap, "dht-bootstrap", "host:port of a DHT node to bootstrap against (repeat for multiple; empty uses anacrolix defaults)")
	dhtInsecure := fs.Bool("dht-insecure", false, "disable BEP-42 node-ID security (TESTING ONLY; needed for private DHTs)")
	regtest := fs.Bool("regtest", false, "accelerated companion/Layer-D publisher timings (TESTING ONLY — never run against mainnet)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: swartznet add <magnet | path.torrent | infohash | ->")
		return exitUsage
	}
	target := fs.Arg(0)

	if *dhtInsecure && !config.UnsafeAuthorized(false) {
		fmt.Fprintln(stderr, "swartznet: --dht-insecure disables BEP-42 node-ID security and is testing-only (set SWARTZNET_UNSAFE=1 to enable)")
		return exitUsage
	}
	if *regtest && !config.UnsafeAuthorized(false) {
		fmt.Fprintln(stderr, "swartznet: --regtest is a testing-only flag (set SWARTZNET_UNSAFE=1 to enable)")
		return exitUsage
	}

	cfg := config.Default()
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *indexDir != "" {
		cfg.IndexDir = *indexDir
	}
	if *port >= 0 { // -1 sentinel keeps the config default; 0 stays meaningful
		cfg.ListenPort = *port
	}
	if *identityPath != "" {
		cfg.IdentityPath = *identityPath
	}
	cfg.DisableDHT = *noDHT
	cfg.DisableDHTPublish = *noDHTPublish
	cfg.NoUpload = *leechOnly
	cfg.DHTBootstrapAddrs = dhtBootstrap
	cfg.DHTInsecure = *dhtInsecure
	cfg.Regtest = *regtest

	log := newLogger(stderr)
	d, err := daemon.New(ctx, daemon.Options{
		Cfg:     cfg,
		Log:     log,
		NoIndex: *noIndex,
		APIAddr: *apiAddr,
		Version: Version,
		Stderr:  stderr,
	})
	if err != nil {
		return reportRunErr(err, stderr)
	}
	defer d.Close()

	if *identityPath != "" && d.Identity == nil {
		fmt.Fprintln(stderr, "swartznet: identity did not load")
		return exitRuntime
	}
	if d.API != nil {
		fmt.Fprintf(stdout, "HTTP API listening on %s\n", d.API.Addr())
	}
	if d.CompPub != nil {
		fmt.Fprintf(stdout, "Companion publisher started, dir=%s\n", cfg.CompanionDir)
	}
	if d.CompSub != nil {
		fmt.Fprintln(stdout, "Companion subscriber started")
	}

	// Input dispatch: magnet | stdin | bare 40-hex infohash | .torrent path.
	var h *engine.Handle
	switch {
	case strings.HasPrefix(target, "magnet:"):
		h, err = d.Eng.AddMagnet(target)
	case target == "-":
		var raw []byte
		raw, err = io.ReadAll(stdin)
		if err == nil {
			h, err = d.Eng.AddTorrentBytes(raw)
		}
	case validInfoHash(strings.ToLower(strings.TrimSpace(target))):
		var hash metainfo.Hash
		if err = hash.FromHexString(strings.ToLower(strings.TrimSpace(target))); err == nil {
			h, err = d.Eng.AddInfoHash(hash)
		}
	default:
		h, err = d.Eng.AddTorrentFile(target)
	}
	if err != nil {
		return reportRunErr(err, stderr)
	}

	fmt.Fprintf(stdout, "Fetching metadata for %s...\n", h.InfoHashHex())
	select {
	case <-h.T.GotInfo():
	case <-ctx.Done():
		// Ctrl-C during the metadata wait: exit 130 with no message (legacy).
		return reportRunErr(ctx.Err(), stderr)
	case <-time.After(5 * time.Minute):
		return reportRunErr(fmt.Errorf("timed out waiting for torrent metadata"), stderr)
	}
	printInfo(stdout, h)
	// No DownloadAll here: the engine's per-file Normal flip on GotInfo is
	// the activation path, and DownloadAll would bypass the queue cap.

	progressLoop(ctx, h, stdout)
	fmt.Fprintln(stdout, "Shutting down...")
	return reportRunErr(ctx.Err(), stderr)
}

func printInfo(w io.Writer, h *engine.Handle) {
	t := h.T
	info := t.Info()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Name:    ", t.Name())
	fmt.Fprintln(w, "  Hash:    ", h.InfoHashHex())
	fmt.Fprintln(w, "  Size:    ", humanBytes(t.Length()))
	if info != nil {
		fmt.Fprintf(w, "  Pieces:   %d x %s\n", info.NumPieces(), humanBytes(info.PieceLength))
	}
	files := t.Files()
	fmt.Fprintln(w, "  Files:   ", len(files))
	for i, f := range files {
		if i == 20 {
			fmt.Fprintf(w, "    ... and %d more\n", len(files)-20)
			break
		}
		fmt.Fprintf(w, "    %s  (%s)\n", f.DisplayPath(), humanBytes(f.Length()))
	}
	fmt.Fprintln(w)
}

// progressLoop prints a status line every 3 s until ctx cancels. The file
// events subscription binds ONCE — every SubscribeFileEvents call creates a
// fresh subscription, and calling it inside the select would hang.
func progressLoop(ctx context.Context, h *engine.Handle, w io.Writer) {
	fileEvents := h.SubscribeFileEvents()
	pieceEvents := h.PieceEvents()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var pieces, filesDone int
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-pieceEvents:
			if !ok {
				pieceEvents = nil // closed: stop selecting, keep serving
				continue
			}
			pieces++
		case ev, ok := <-fileEvents:
			if !ok {
				fileEvents = nil // closed: stop selecting, keep serving
				continue
			}
			filesDone++
			fmt.Fprintf(w, "  ✓ file complete: %s  (%s)\n", ev.Path, humanBytes(ev.Size))
		case <-ticker.C:
			var pct float64
			if total := h.T.Length(); total > 0 {
				pct = 100 * float64(h.T.BytesCompleted()) / float64(total)
			}
			stats := h.T.Stats()
			fmt.Fprintf(w, "[%s] %5.1f%%  %s / %s  peers=%d/%d  piece-events=%d  files-done=%d\n",
				time.Now().Format("15:04:05"), pct,
				humanBytes(h.T.BytesCompleted()), humanBytes(h.T.Length()),
				stats.ActivePeers, stats.TotalPeers, pieces, filesDone)
		}
	}
}

// humanBytes renders binary sizes: "512 B", "1.5 KiB", "2.0 GiB".
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(1024), 0
	for u := n / 1024; u >= 1024; u /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// validInfoHash reports whether s is exactly 40 hex characters.
func validInfoHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// stringSliceFlag collects repeatable string flags.
type stringSliceFlag []string

func (f *stringSliceFlag) String() string { return strings.Join(*f, ",") }
func (f *stringSliceFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}
