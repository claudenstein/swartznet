package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/httpapi"
	"github.com/swartznet/swartznet/internal/indexer"
)

// cmdAdd implements `swartznet add <magnet-uri | path.torrent>`.
//
// For M1 this is both a user-facing command and the main smoke test for
// the Engine wrapper: it validates that we can construct a Client, add a
// torrent, receive its metadata, subscribe to piece-state changes, and
// shut down cleanly on Ctrl-C.
func cmdAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dataDir   string
		indexDir  string
		port      int
		noDHT     bool
		leechOnly bool
		noIndex   bool
		apiAddr   string
	)
	fs.StringVar(&dataDir, "data-dir", "", "data directory for downloaded content")
	fs.StringVar(&indexDir, "index-dir", "", "Bleve index directory (default: ~/.local/share/swartznet/index)")
	fs.IntVar(&port, "port", -1, "listen port (0 = OS-assigned)")
	fs.BoolVar(&noDHT, "no-dht", false, "disable the mainline DHT")
	fs.BoolVar(&leechOnly, "leech-only", false, "disable uploading (debug)")
	fs.BoolVar(&noIndex, "no-index", false, "don't write this torrent to the local index")
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "HTTP API listen address (empty to disable)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: swartznet add <magnet-uri | path.torrent>")
		return exitUsage
	}
	target := fs.Arg(0)

	cfg := config.Default()
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	if indexDir != "" {
		cfg.IndexDir = indexDir
	}
	if port >= 0 {
		cfg.ListenPort = port
	}
	cfg.DisableDHT = noDHT
	cfg.NoUpload = leechOnly

	log := newLogger(stderr)
	ctx, cancel := signalContext(context.Background())
	defer cancel()

	eng, err := engine.New(ctx, cfg, log)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	defer func() { _ = eng.Close() }()

	var idx *indexer.Index
	if !noIndex {
		var err error
		idx, err = indexer.Open(cfg.IndexDir)
		if err != nil {
			return reportRunErr(fmt.Errorf("open index: %w", err), stderr)
		}
		defer idx.Close()
		eng.SetIndex(idx)
	}

	// Start the local HTTP API so `swartznet search --swarm` in
	// another terminal can talk to this running daemon. Empty
	// --api-addr disables it entirely. The API now also exposes
	// the M4 publisher and DHT lookup so search --dht and the
	// status command both work end-to-end.
	if apiAddr != "" {
		api := httpapi.NewWithOptions(apiAddr, log, httpapi.Options{
			Index:     idx,
			Swarm:     eng.SwarmSearch(),
			Publisher: eng.Publisher(),
			Lookup:    eng.Lookup(),
		})
		if err := api.Start(); err != nil {
			fmt.Fprintf(stderr, "warning: httpapi start failed: %v\n", err)
		} else {
			defer func() {
				shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = api.Stop(shutdown)
			}()
			fmt.Fprintf(stdout, "HTTP API listening on %s\n", api.Addr())
		}
	}

	h, err := addTorrent(eng, target)
	if err != nil {
		return reportRunErr(err, stderr)
	}

	fmt.Fprintf(stdout, "Fetching metadata for %s...\n", h.T.InfoHash().HexString())
	select {
	case <-h.T.GotInfo():
		// metadata arrived
	case <-ctx.Done():
		return exitInterrupt
	case <-time.After(5 * time.Minute):
		return reportRunErr(fmt.Errorf("timed out waiting for torrent metadata"), stderr)
	}

	printInfo(stdout, h)

	// Start the download of all pieces. For M1 we download everything; M2
	// will add selective file downloading driven by the local index.
	h.T.DownloadAll()

	// Live progress loop: every few seconds we print a one-line status until
	// the context is cancelled. In M2 we will route the piece-events channel
	// into the indexer; for M1 we just drain it to prove it works.
	progressLoop(ctx, stdout, h)

	fmt.Fprintln(stdout, "Shutting down...")
	return reportRunErr(ctx.Err(), stderr)
}

// addTorrent dispatches between magnet URIs and .torrent file paths based on
// the argument prefix. Any other input is rejected.
func addTorrent(eng *engine.Engine, target string) (*engine.Handle, error) {
	if strings.HasPrefix(target, "magnet:") {
		return eng.AddMagnet(target)
	}
	// Accept a .torrent file path. We don't stat it here; engine will
	// surface a useful error if the path is wrong.
	return eng.AddTorrentFile(target)
}

func printInfo(w io.Writer, h *engine.Handle) {
	t := h.T
	info := t.Info()
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  Name:    ", t.Name())
	fmt.Fprintln(w, "  Hash:    ", t.InfoHash().HexString())
	fmt.Fprintln(w, "  Size:    ", humanBytes(t.Length()))
	if info != nil {
		fmt.Fprintln(w, "  Pieces:  ", info.NumPieces(), "x", humanBytes(info.PieceLength))
	}
	files := t.Files()
	fmt.Fprintln(w, "  Files:   ", len(files))
	const maxListed = 20
	for i, f := range files {
		if i == maxListed {
			fmt.Fprintf(w, "    ... and %d more\n", len(files)-maxListed)
			break
		}
		fmt.Fprintf(w, "    %s  (%s)\n", f.DisplayPath(), humanBytes(f.Length()))
	}
	fmt.Fprintln(w, "")
}

// progressLoop prints a one-line status every few seconds while draining
// both the piece-events and file-complete channels. Returns when ctx is
// cancelled. File completions are reported inline so the user can see the
// M2.1 tracker working end-to-end against a real torrent.
func progressLoop(ctx context.Context, w io.Writer, h *engine.Handle) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	var piecesSeen, filesSeen int
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.PieceEvents():
			piecesSeen++
		case ev, ok := <-h.FileEvents():
			if !ok {
				// Tracker shut down (engine closed); keep the loop alive
				// until ctx cancels so the UI stops cleanly.
				continue
			}
			filesSeen++
			fmt.Fprintf(w, "  ✓ file complete: %s  (%s)\n", ev.Path, humanBytes(ev.Size))
		case <-tick.C:
			s := h.T.Stats()
			total := h.T.Length()
			have := h.T.BytesCompleted()
			var pct float64
			if total > 0 {
				pct = 100 * float64(have) / float64(total)
			}
			fmt.Fprintf(w,
				"[%s] %5.1f%%  %s / %s  peers=%d/%d  piece-events=%d  files-done=%d\n",
				time.Now().Format("15:04:05"),
				pct,
				humanBytes(have),
				humanBytes(total),
				s.ActivePeers,
				s.TotalPeers,
				piecesSeen,
				filesSeen,
			)
		}
	}
}

// humanBytes formats a byte count with binary prefixes.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
