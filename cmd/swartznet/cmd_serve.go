package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/daemon"
)

// cmdServe is the TEMPORARY walking-skeleton scaffold: it stands up the
// daemon lifecycle spine with no engine behind it. A later slice deletes this
// command and folds the spine into 'add' — in SwartzNet, 'add' is the daemon.
func cmdServe(args []string, stdout, stderr io.Writer) int {
	ctx, cancel := signalContext(context.Background())
	defer cancel()
	return serveWithContext(ctx, args, stdout, stderr)
}

// serveWithContext is cmdServe minus signal wiring, so tests can drive the
// full lifecycle with their own cancellable context.
func serveWithContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiAddr := fs.String("api-addr", "localhost:7654", `HTTP API listen address ("" to disable)`)
	dataDir := fs.String("data-dir", "", "download/data directory (default: XDG data dir)")
	indexDir := fs.String("index-dir", "", "search index directory (default: XDG data dir)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	cfg := config.Default()
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *indexDir != "" {
		cfg.IndexDir = *indexDir
	}

	log := newLogger(stderr)
	d, err := daemon.New(ctx, daemon.Options{
		Cfg:     cfg,
		Log:     log,
		APIAddr: *apiAddr,
		Version: Version,
		Stderr:  stderr,
	})
	if err != nil {
		return reportRunErr(err, stderr)
	}
	defer d.Close()

	if *apiAddr != "" && d.API == nil {
		// The bind failed (daemon.New already warned on stderr). A scaffold
		// serve with no API serves nothing, so fail loudly; the future 'add'
		// instead keeps running degraded.
		fmt.Fprintln(stderr, "swartznet: http api did not start")
		return exitRuntime
	}
	if d.API != nil {
		fmt.Fprintf(stdout, "HTTP API listening on %s\n", d.API.Addr())
	}

	<-ctx.Done()
	fmt.Fprintln(stdout, "Shutting down...")
	return reportRunErr(ctx.Err(), stderr)
}
