package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/engine"
	"github.com/swartznet/swartznet/internal/identity"
)

// cmdCreate builds a .torrent, optionally signs it, and optionally seeds the
// content in place. It is a one-shot tool: it never constructs daemon.New,
// and without --seed it builds no engine at all — a pure hashing run.
func cmdCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "output .torrent path (required)")
	name := fs.String("name", "", "override the info.name field (default: basename of root)")
	pieceKiB := fs.Int64("piece-kib", 0, "piece length in KiB (0 = auto)")
	var trackers, webseeds stringSliceFlag
	fs.Var(&trackers, "tracker", "tracker announce URL (repeat for multiple)")
	fs.Var(&webseeds, "webseed", "webseed URL (repeat for multiple)")
	comment := fs.String("comment", "", "optional torrent comment")
	private := fs.Bool("private", false, "mark as private (BEP-27: disables DHT/PEX)")
	seed := fs.Bool("seed", false, "after creation, start seeding the content")
	dataDir := fs.String("data-dir", "", "with --seed, directory for session state (content is seeded in place from <root>; default: ~/.local/share/swartznet)")
	signFlag := fs.Bool("sign", false, "sign the .torrent file with our ed25519 identity so downloaders running SwartzNet can verify the publisher")
	identityPath := fs.String("identity", "", "path to the ed25519 identity.key file (load-only unless it is the default path, which is auto-created)")
	noDHT := fs.Bool("no-dht", false, "with --seed, disable the DHT and gateway port mapping (direct peers only)")
	pos, err := parseFlagsAllowingLeadingPositionals(fs, args)
	if err != nil {
		return parseErrExit(err)
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: swartznet create <file-or-folder> -o <output.torrent>")
		return exitUsage
	}
	root := pos[0]
	if *out == "" {
		fmt.Fprintln(stderr, "swartznet: -o <output.torrent> is required")
		return exitUsage
	}
	if *identityPath != "" && !*signFlag {
		// Fail closed: silently ignoring would let the user believe their
		// chosen key signed the torrent.
		fmt.Fprintln(stderr, "swartznet: --identity is only used with --sign (pass --sign to sign, or drop --identity)")
		return exitUsage
	}

	// Guard the multiply: an absurd --piece-kib must fail closed, not wrap
	// around int64 and sneak past the engine's power-of-two validation.
	if *pieceKiB < 0 || *pieceKiB > (1<<40) {
		fmt.Fprintln(stderr, "swartznet: --piece-kib out of range")
		return exitUsage
	}

	opts := engine.CreateTorrentOptions{
		Root:        root,
		Name:        *name,
		PieceLength: *pieceKiB * 1024,
		Trackers:    trackers,
		WebSeeds:    webseeds,
		Private:     *private,
		Comment:     *comment,
		CreatedBy:   "swartznet " + Version,
	}

	if *signFlag {
		// Identity loads BEFORE any hashing so a bad key path fails fast.
		// Explicit non-default paths are load-only (never mint a key that
		// would orphan the torrent under an untrusted pubkey).
		idPath := *identityPath
		if idPath == "" {
			idPath = config.Default().IdentityPath
		}
		allowCreate := filepath.Clean(idPath) == config.Default().IdentityPath
		id, err := identity.Load(idPath, allowCreate)
		if err != nil {
			return reportRunErr(fmt.Errorf("load identity: %w", err), stderr)
		}
		signer := id.Signer()
		opts.SignWith = &signer
		fmt.Fprintf(stdout, "Signing with identity %s\n", id.PublicKeyHex())
	}

	fmt.Fprintf(stdout, "Hashing %s...\n", root)
	ihHex, rawBytes, err := engine.CreateTorrentFile(opts, *out)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintf(stdout, "✓ Created %s\n", *out)
	fmt.Fprintf(stdout, "  InfoHash: %s\n", ihHex)
	if !*seed {
		return exitOK
	}

	// --seed: a daemonless engine (no API, no indexer) with DHT + upload on
	// so a trackerless seed is discoverable; ephemeral port so a running
	// daemon on the default port is never clashed with.
	cfg := config.Default()
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	cfg.ListenPort = 0
	cfg.DisableDHT = *noDHT
	cfg.DisablePortForwarding = *noDHT // a DHT-less seed is a local seed
	cfg.NoUpload = false
	eng, err := engine.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return reportRunErr(fmt.Errorf("create engine: %w", err), stderr)
	}
	defer eng.Close()

	// The FINAL (possibly signed) bytes are what get seeded and persisted,
	// so the creator's own node shows SignedBy — the legacy re-marshaled the
	// unsigned struct here and never saw its own signature.
	if _, err := eng.AddTorrentBytesSeedFrom(rawBytes, root); err != nil {
		fmt.Fprintf(stderr, "warning: seed start failed: %v\n", err)
		return exitRuntime
	}
	fmt.Fprintln(stdout, "Seeding... (Ctrl-C to stop)")
	ctx, cancel := signalContext(context.Background())
	defer cancel()
	<-ctx.Done()
	return reportRunErr(ctx.Err(), stderr)
}
