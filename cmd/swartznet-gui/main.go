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
		loadFiles    torrentFileFlag
		startTab     string
	)
	fs.StringVar(&dataDir, "data-dir", "", "data directory for downloaded content")
	fs.StringVar(&indexDir, "index-dir", "", "Bleve index directory")
	fs.IntVar(&port, "port", -1, "listen port (0 = OS-assigned)")
	fs.BoolVar(&noDHT, "no-dht", false, "disable the mainline DHT entirely")
	fs.BoolVar(&noDHTPublish, "no-dht-publish", false, "join DHT but don't publish BEP-44 items")
	fs.StringVar(&apiAddr, "api-addr", "localhost:7654", "HTTP API listen address (empty to disable)")
	fs.Var(&loadFiles, "torrent", "load a .torrent file at startup (repeat for multiple)")
	fs.StringVar(&startTab, "tab", "", "open a specific tab at startup (downloads|search|status|companion|settings)")
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

	// Auto-load any .torrent files the user passed with --torrent.
	// Useful for demos, screenshots, and re-launching the GUI
	// pre-populated with a known set of torrents.
	for _, path := range loadFiles {
		if _, err := d.Eng.AddTorrentFile(path); err != nil {
			fmt.Fprintf(stderr, "warning: load %s: %v\n", path, err)
		} else {
			fmt.Fprintf(stdout, "Loaded %s\n", path)
		}
	}

	app := gui.New(d, Version)
	if startTab != "" {
		app.SelectTab(startTab)
	}
	app.Run()
	app.Cleanup()

	return 0
}

// torrentFileFlag implements flag.Value for repeated --torrent flags.
type torrentFileFlag []string

func (t *torrentFileFlag) String() string     { return fmt.Sprintf("%v", []string(*t)) }
func (t *torrentFileFlag) Set(v string) error { *t = append(*t, v); return nil }

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
