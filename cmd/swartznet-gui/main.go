// Command swartznet-gui is the Fyne-based native GUI for SwartzNet. It
// constructs a full daemon.Daemon (engine, indexer, companion, optional HTTP
// API) and hands it to internal/gui, which is pure presentation — the GUI adds
// no independent lifecycle or reconciliation logic.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/gui"
)

var (
	// Version and BuildDate are the SINGLE build-stamped source of truth,
	// overridden via -ldflags by build-gui.sh / build-release.sh. The GUI's
	// About dialog and window title read only these.
	Version   = "v0.11.0-dev"
	BuildDate = ""
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("swartznet-gui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "data directory for downloaded content (default: XDG data dir)")
	indexDir := fs.String("index-dir", "", "Bleve index directory (default: XDG data dir)")
	port := fs.Int("port", -1, "BitTorrent listen port (0 = OS-assigned)")
	noDHT := fs.Bool("no-dht", false, "disable the mainline DHT entirely")
	noDHTPublish := fs.Bool("no-dht-publish", false, "stay on the DHT but suppress Layer-D publishing")
	noIndex := fs.Bool("no-index", false, "don't index downloaded content at all")
	apiAddr := fs.String("api-addr", "localhost:7654", "HTTP API listen address (empty to disable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := config.Default()
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *indexDir != "" {
		cfg.IndexDir = *indexDir
	}
	if *port >= 0 {
		cfg.ListenPort = *port
	}
	cfg.DisableDHT = *noDHT
	cfg.DisableDHTPublish = *noDHTPublish

	log := newLogger(stderr)
	d, err := daemon.New(context.Background(), daemon.Options{
		Cfg:     cfg,
		Log:     log,
		NoIndex: *noIndex,
		APIAddr: *apiAddr,
		Version: Version,
		Stderr:  stderr,
	})
	if err != nil {
		fmt.Fprintln(stderr, "swartznet-gui:", err)
		return 1
	}
	// gui.New takes ownership of the daemon lifecycle (Close on window close).
	gui.New(d, Version, BuildDate).Run()
	return 0
}

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
