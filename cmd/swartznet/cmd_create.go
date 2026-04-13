package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/identity"
)

// cmdCreate implements `swartznet create <root> -o <output.torrent>`.
// Builds a new .torrent file from the content at <root> (file or
// directory), writes it to <output>, and prints the resulting
// infohash.
//
// Unlike search/status/flag, this command does NOT talk to a
// running daemon — piece hashing is synchronous and CPU-bound, so
// doing it in-process is simpler and avoids shoving multi-GiB file
// contents through a local HTTP pipe.
func cmdCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		out          string
		name         string
		pieceKiB     int64
		trackers     stringSliceFlag
		webseeds     stringSliceFlag
		comment      string
		private      bool
		startSeed    bool
		dataDir      string
		sign         bool
		identityPath string
	)
	fs.StringVar(&out, "o", "", "output .torrent path (required)")
	fs.StringVar(&name, "name", "", "override the info.name field (default: basename of root)")
	fs.Int64Var(&pieceKiB, "piece-kib", 0, "piece length in KiB (0 = auto)")
	fs.Var(&trackers, "tracker", "tracker announce URL (repeat for multiple)")
	fs.Var(&webseeds, "webseed", "webseed URL (repeat for multiple)")
	fs.StringVar(&comment, "comment", "", "optional torrent comment")
	fs.BoolVar(&private, "private", false, "mark as private (BEP-27: disables DHT/PEX)")
	fs.BoolVar(&startSeed, "seed", false, "after creation, start seeding the content")
	fs.StringVar(&dataDir, "data-dir", "", "data directory for seeding (required if --seed, must contain the root)")
	fs.BoolVar(&sign, "sign", false, "sign the .torrent file with our ed25519 identity so downloaders running SwartzNet can verify the publisher")
	fs.StringVar(&identityPath, "identity", "", "path to the ed25519 identity.key file (defaults to ~/.local/share/swartznet/identity.key)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: swartznet create <file-or-folder> -o <output.torrent>")
		return exitUsage
	}
	root := fs.Arg(0)
	if out == "" {
		fmt.Fprintln(stderr, "swartznet: -o <output.torrent> is required")
		return exitUsage
	}

	// Build CreateTorrentOptions.
	opts := engine.CreateTorrentOptions{
		Root:        root,
		Name:        name,
		PieceLength: pieceKiB * 1024,
		Trackers:    []string(trackers),
		WebSeeds:    []string(webseeds),
		Private:     private,
		Comment:     comment,
		CreatedBy:   "swartznet " + Version,
	}

	// Load the signing identity if --sign was requested. We load
	// BEFORE spinning up the engine so a bad key path fails fast
	// without starting piece hashing.
	if sign {
		cfg := config.Default()
		path := identityPath
		if path == "" {
			path = cfg.IdentityPath
		}
		id, err := identity.LoadOrCreate(path)
		if err != nil {
			return reportRunErr(fmt.Errorf("load identity: %w", err), stderr)
		}
		opts.SignWith = id.PrivateKey
		fmt.Fprintf(stdout, "Signing with identity %s\n", id.PublicKeyHex())
	}

	// We don't need a running engine for CreateTorrent, but the
	// current API requires an *Engine receiver. Spin up a minimal
	// one (no DHT, no index, no upload) — it's about 500 ms of
	// overhead on my laptop, well worth the code simplicity.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	cfg.DisableDHT = true
	cfg.ListenPort = 0
	cfg.NoUpload = !startSeed

	eng, err := engine.New(context.Background(), cfg, log)
	if err != nil {
		return reportRunErr(fmt.Errorf("create engine: %w", err), stderr)
	}
	defer eng.Close()

	fmt.Fprintf(stdout, "Hashing %s...\n", root)
	ih, mi, err := eng.CreateTorrentFile(opts, out)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintf(stdout, "✓ Created %s\n", out)
	fmt.Fprintf(stdout, "  InfoHash: %s\n", ih)

	if startSeed {
		if _, err := eng.AddTorrentMetaInfo(mi); err != nil {
			fmt.Fprintf(stderr, "warning: seed start failed: %v\n", err)
			return exitRuntime
		}
		fmt.Fprintln(stdout, "Seeding... (Ctrl-C to stop)")
		ctx, cancel := signalContext(context.Background())
		defer cancel()
		<-ctx.Done()
	}
	return exitOK
}

// stringSliceFlag implements flag.Value for repeated string flags.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}
