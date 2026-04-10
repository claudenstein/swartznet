// Command swartznet is the CLI entry point for the SwartzNet BitTorrent
// client with built-in distributed text search.
//
// M1 only implements the engine smoke-test commands: `version`, `add`, and
// `help`. Later milestones will add `search`, `index`, `publish`, and a REST
// API sub-server.
//
// The full architecture and roadmap is in docs/05-integration-design.md.
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

// Version is the short SwartzNet version string. It is overridden at build
// time with -ldflags "-X main.Version=..." in release builds. The default is
// a human-readable placeholder so unlabelled dev builds still print something
// meaningful.
var Version = "0.0.1-dev"

// exitCode values are documented here so shell scripts can rely on them.
const (
	exitOK        = 0
	exitUsage     = 2
	exitRuntime   = 1
	exitInterrupt = 130
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the real entry point; main is a thin shell. Keeping run testable
// (takes args and writers) means we can smoke-test the CLI in-process later.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	cmd, rest := args[0], args[1:]

	switch cmd {
	case "help", "-h", "--help":
		printUsage(stdout)
		return exitOK
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "swartznet", Version)
		return exitOK
	case "add":
		return cmdAdd(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "swartznet: unknown command %q\n\n", cmd)
		printUsage(stderr)
		return exitUsage
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `swartznet — BitTorrent client with built-in distributed text search

Usage:
  swartznet <command> [arguments]

Commands:
  add <magnet-uri | path.torrent>   Add a torrent and start downloading (Ctrl-C to stop).
  version                           Print the version and exit.
  help                              Print this message.

Flags for 'add':
  --data-dir <path>   Override the data directory (default: ~/.local/share/swartznet/data).
  --port <int>        Override the listen port (default: 42069, 0 = OS-assigned).
  --no-dht            Disable the mainline DHT.
  --leech-only        Do not upload to peers (debugging only).

Documentation:
  Research reports and the full design are in the docs/ directory.
  The authoritative design document is docs/05-integration-design.md.

`)
}

// newLogger returns a structured logger configured from SWARTZNET_LOG.
// Values: "debug", "info" (default), "warn", "error".
func newLogger(w io.Writer) *slog.Logger {
	lvl := slog.LevelInfo
	switch os.Getenv("SWARTZNET_LOG") {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl}))
}

// signalContext returns a context that is cancelled on SIGINT or SIGTERM.
// The returned cancel func restores the default signal handlers; always
// call it.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
	return ctx, cancel
}

// reportRunErr maps a runtime error to a useful exit code.
func reportRunErr(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, context.Canceled) {
		// Clean shutdown via SIGINT.
		return exitInterrupt
	}
	fmt.Fprintln(stderr, "swartznet:", err)
	return exitRuntime
}
