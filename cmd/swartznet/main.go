// Command swartznet is the SwartzNet CLI: a mainline-compatible BitTorrent
// client with built-in distributed full-text search. The embedded web UI is
// served by the same binary through the localhost HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Exit codes are a documented contract so shell scripts can rely on them.
const (
	exitOK        = 0
	exitRuntime   = 1
	exitUsage     = 2
	exitInterrupt = 130
)

// Version is stamped by release builds via -ldflags "-X main.Version=...";
// dev builds keep this placeholder.
var Version = "v0.9.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a CLI invocation and returns its exit code. It is the
// testable entry point: tests drive the whole CLI in-process through it.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return exitOK
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "swartznet", Version)
		return exitOK
	case "add":
		return cmdAdd(args[1:], os.Stdin, stdout, stderr)
	case "create":
		return cmdCreate(args[1:], stdout, stderr)
	case "status":
		return cmdStatus(args[1:], stdout, stderr)
	case "files":
		return cmdFiles(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "swartznet: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return exitUsage
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `swartznet — BitTorrent client with built-in distributed text search

Usage:

  swartznet <command> [flags]

Commands:

  add <target>         Add a torrent and run the node (Ctrl-C to stop). The
                       target is a magnet URI, a .torrent path, a bare 40-hex
                       infohash, or - for .torrent bytes on stdin.
  create <path> -o <t> Create a .torrent from a file or folder. --sign adds a
                       publisher signature (infohash unchanged); --seed then
                       seeds the content in place.
  status               Show a running daemon's status over its HTTP API.
  files <infohash> [<idx> <prio>]
                       List a torrent's files, or set one file's priority
                       (none/normal/high).
  version              Print the version.
  help                 Show this help.

Flags for 'add':

  --api-addr <addr>    HTTP API listen address (default: localhost:7654, "" to disable).
  --data-dir <path>    Download/data directory (default: XDG data dir).
  --index-dir <path>   Search index directory (default: XDG data dir).
  --identity <path>    ed25519 identity.key file. Load-only unless it names the
                       default XDG path, which is auto-created on first run;
                       any other missing path is an error.
  --port <n>           BitTorrent listen port (default: 42069, 0 = OS-assigned).
  --no-dht             Disable the mainline DHT entirely.
  --dht-bootstrap <hp> DHT bootstrap node host:port (repeatable).
  --dht-insecure       Disable BEP-42 node-ID security (testing only; gated).
  --leech-only         Disable uploading (debug).
  --no-index           Don't index downloaded content at all.

Flags for 'create':

  -o <path>            Output .torrent path (required).
  --name <s>           Override info.name (default: basename of root).
  --piece-kib <n>      Piece length in KiB (0 = auto; else power of two ≥ 16).
  --tracker <url>      Tracker announce URL (repeatable; default trackerless).
  --webseed <url>      Webseed URL (repeatable).
  --comment <s>        Optional torrent comment.
  --private            Mark as private (BEP-27: disables DHT/PEX).
  --sign               Sign with the node identity (snet.* top-level fields).
  --identity <path>    Identity key for --sign (same load-only rule as 'add').
  --seed               Seed the content in place after creation.
  --data-dir <path>    With --seed, session-state directory.
  --no-dht             With --seed, disable DHT + port mapping (direct peers).

Flags for 'status' and 'files':

  --api-addr <addr>    Address of the running swartznet HTTP API (default: localhost:7654).
  --json               Emit JSON instead of text.

Environment:

  SWARTZNET_LOG        Log verbosity: debug|info|warn|error (default: info). Structured logs go to stderr.
  SWARTZNET_UNSAFE     Set to 1 to allow test-only knobs (regtest / insecure DHT) outside 'go test'.

Documentation:

  https://github.com/swartznet/swartznet
`)
}

// newLogger builds the process logger from SWARTZNET_LOG (debug|info|warn|
// error, default info), writing slog text lines to w. The GUI's logger must
// stay behavior-identical to this one.
func newLogger(w io.Writer) *slog.Logger {
	v := os.Getenv("SWARTZNET_LOG")
	lvl := slog.LevelInfo
	recognized := true
	switch v {
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		// default
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		recognized = false
	}
	log := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl}))
	if !recognized {
		log.Warn("unrecognized SWARTZNET_LOG value, using info", "value", v)
	}
	return log
}

// signalContext derives a context cancelled by the first SIGINT/SIGTERM.
// After the first signal the default disposition is restored, so a second
// Ctrl-C during a hung teardown kills the process immediately.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(sigCh)
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// reportRunErr maps a command's terminal error to an exit code: nil → 0,
// context.Canceled (clean signal shutdown) → 130 silently, anything else →
// "swartznet: <err>" on stderr and 1.
func reportRunErr(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, context.Canceled) {
		return exitInterrupt
	}
	fmt.Fprintln(stderr, "swartznet:", err)
	return exitRuntime
}
