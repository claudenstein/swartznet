// Command swartznet-gui is the Fyne-based graphical interface for
// the SwartzNet BitTorrent client with built-in distributed text
// search. It starts a full daemon (engine, indexer, companion,
// optional HTTP API) and presents the UI in a native window.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
	"github.com/swartznet/swartznet/internal/gui"
)

var Version = "0.0.1-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("swartznet-gui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dataDir      string
		indexDir     string
		port         int
		noDHT        bool
		noDHTPublish bool
		apiAddr      string
	)
	fs.StringVar(&dataDir, "data-dir", "", "data directory for downloaded content")
	fs.StringVar(&indexDir, "index-dir", "", "Bleve index directory")
	fs.IntVar(&port, "port", -1, "listen port (0 = OS-assigned)")
	fs.BoolVar(&noDHT, "no-dht", false, "disable the mainline DHT entirely")
	fs.BoolVar(&noDHTPublish, "no-dht-publish", false, "join DHT but don't publish BEP-44 items")
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "HTTP API listen address (empty to disable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

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
	cfg.DisableDHTPublish = noDHTPublish

	log := newLogger(stderr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	d, err := daemon.New(ctx, daemon.Options{
		Cfg:     cfg,
		Log:     log,
		APIAddr: apiAddr,
		Version: Version,
		Stderr:  stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "swartznet-gui: %v\n", err)
		return 1
	}
	defer d.Close()

	if d.API != nil {
		fmt.Fprintf(stdout, "HTTP API listening on %s\n", d.API.Addr())
	}

	app := gui.New(d, Version)
	app.Run()
	app.Cleanup()

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
