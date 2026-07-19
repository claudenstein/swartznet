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
	case "search":
		return cmdSearch(args[1:], stdout, stderr)
	case "index":
		return cmdIndex(args[1:], stdout, stderr)
	case "trust":
		return cmdTrust(args[1:], stdout, stderr)
	case "confirm":
		return cmdConfirm(args[1:], stdout, stderr)
	case "flag":
		return cmdFlag(args[1:], stdout, stderr)
	case "crawl-probe":
		return cmdCrawlProbe(args[1:], stdout, stderr)
	case "companion":
		return cmdCompanion(args[1:], stdout, stderr)
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
  search <query...>    Full-text search the local index (Layer L). --signed-by,
                       --swarm, --dht route through the daemon.
  index [<ih> on|off]  Show index stats, or toggle a torrent's indexing.
  trust <list|add|remove>
                       Manage the publisher allowlist (offline; no daemon).
  confirm <infohash>   Mark a hit as good (adds it to the known-good filter).
  flag <infohash>      Report a hit as bad (demotes its attributed indexers).
  companion <status|follow|unfollow|refresh>
                       Manage the companion content-index: show publish/follow
                       state, follow/unfollow a publisher pubkey, or re-publish.
  crawl-probe --addr <host:port>
                       One-shot BEP-51 sample_infohashes probe of a DHT node
                       (ops diagnostic; no daemon). --target, --timeout-ms, --json.
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
  --no-dht-publish     Stay on the DHT but suppress Layer-D keyword publishing
                       under your identity (privacy; leech-only Layer D).
  --dht-bootstrap <hp> DHT bootstrap node host:port (repeatable).
  --dht-insecure       Disable BEP-42 node-ID security (testing only; gated).
  --regtest            Accelerated companion/Layer-D timings (testing only; gated).
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

Flags for 'status', 'files', 'confirm' and 'flag':

  --api-addr <addr>    Address of the running swartznet HTTP API (default: localhost:7654).
  --json               Emit JSON instead of text ('status' and 'files' only).

Flags for 'search':

  --limit <n>          Max results (default: 20).
  --signed-by <hex>    Restrict local hits to a 64-hex publisher (routes via daemon).
  --swarm              Also query connected peers (Layer S; routes via daemon).
  --dht                Also query the DHT keyword index (Layer D; routes via daemon).
  --swarm-timeout-ms <n> Layer-S timeout (default: 2000).
  --dht-timeout-ms <n> Layer-D timeout (default: 5000).
  --api-addr <addr>    Daemon HTTP API address (default: localhost:7654).
  --index-dir <path>   Bleve index directory for a direct (no-daemon) local search.
  --json               Emit JSON instead of text.

  Example: swartznet search --dht --json new ubuntu

Flags for 'crawl-probe':

  --addr <host:port>   DHT node to probe (required).
  --target <hex>       20-byte (40-hex) node-ID target (default: random each run).
  --timeout-ms <n>     Query timeout in milliseconds (default: 5000).
  --json               Emit JSON (samples/nodes as hex, interval, num-tracked).

  Example: swartznet crawl-probe --addr 127.0.0.1:6881 --json

Flags for 'companion':

  status  [--api-addr <addr>] [--json]   Show publisher + follow state.
  follow   <pubkey-hex> [--label <name>] [--api-addr <addr>]   Follow a publisher.
  unfollow <pubkey-hex> [--api-addr <addr>]                    Unfollow a publisher.
  refresh [--api-addr <addr>]            Force an immediate re-publish.

  The companion publisher/subscriber run automatically inside 'add' when a
  companion dir is configured (the default). Followed publishers persist in
  ~/.local/share/swartznet/companion-follows.json.

  Example: swartznet companion follow 0123…def --label official-seed

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
