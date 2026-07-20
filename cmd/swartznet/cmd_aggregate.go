package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/swartznet/swartznet/contracts/snagg"
)

// cmdAggregate is the offline Aggregate (PPMI + SNAGG B-tree) ops tooling. It
// never touches the daemon, DHT, or network.
func cmdAggregate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "swartznet aggregate: missing subcommand")
		printAggregateUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "build":
		return cmdAggregateBuild(args[1:], stdout, stderr)
	case "inspect":
		return cmdAggregateInspect(args[1:], stdout, stderr)
	case "find":
		return cmdAggregateFind(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printAggregateUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "swartznet aggregate: unknown subcommand %q\n", args[0])
		printAggregateUsage(stderr)
		return exitUsage
	}
}

func printAggregateUsage(w io.Writer) {
	fmt.Fprint(w, `swartznet aggregate — Aggregate (PPMI + B-tree) ops tooling

Usage:
  swartznet aggregate <subcommand> [args]

Subcommands:
  build  --out=FILE [flags]         Sign + pack JSONL records into a signed B-tree index.
  inspect <index-file>              Print trailer metadata for an Aggregate index.
  find <index-file> <prefix>        List records matching a keyword prefix.
  help                              Print this message.

Examples:
  swartznet aggregate build --in=recs.jsonl --out=idx.snagg --seq=7 --pow-bits=20
  swartznet aggregate inspect idx.snagg
  swartznet aggregate find --verify idx.snagg ubu
`)
}

func cmdAggregateInspect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aggregate inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pieceSize := fs.Int("piece-size", snagg.MinPieceSize, "piece size in bytes; must match the torrent's metainfo")
	if err := fs.Parse(args); err != nil {
		return parseErrExit(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: swartznet aggregate inspect <index-file>")
		return exitUsage
	}
	if *pieceSize <= 0 {
		fmt.Fprintln(stderr, "swartznet: --piece-size must be positive")
		return exitUsage
	}
	path := fs.Arg(0)
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: read %s: %v\n", path, err)
		return exitRuntime
	}
	tree, err := snagg.OpenBTree(snagg.BytesPageSource{Data: data, PieceSize: *pieceSize})
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: open b-tree: %v\n", err)
		return exitRuntime
	}
	tr := tree.Trailer
	fmt.Fprintln(stdout, "Aggregate index inspection")
	fmt.Fprintf(stdout, "  file:           %s\n", path)
	fmt.Fprintf(stdout, "  file size:      %d bytes\n", len(data))
	fmt.Fprintf(stdout, "  piece size:     %d bytes\n", *pieceSize)
	fmt.Fprintf(stdout, "  pages:          %d\n", tr.NumPages)
	fmt.Fprintf(stdout, "  records:        %d\n", tr.NumRecords)
	fmt.Fprintf(stdout, "  publisher pk:   %s\n", hex.EncodeToString(tr.PubKey[:]))
	fmt.Fprintf(stdout, "  sequence:       %d\n", tr.Seq)
	fmt.Fprintf(stdout, "  created:        %d (unix)\n", tr.CreatedTs)
	fmt.Fprintf(stdout, "  min PoW bits:   %d\n", tr.MinPoWBits)
	fmt.Fprintf(stdout, "  fingerprint:    %s\n", hex.EncodeToString(tr.Fingerprint[:]))
	return exitOK
}

func cmdAggregateFind(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aggregate find", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pieceSize := fs.Int("piece-size", snagg.MinPieceSize, "piece size in bytes; must match the torrent's metainfo")
	verify := fs.Bool("verify", false, "also run VerifyFingerprint (scans every leaf; slower)")
	if err := fs.Parse(args); err != nil {
		return parseErrExit(err)
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: swartznet aggregate find [--piece-size=N] [--verify] <index-file> <prefix>")
		return exitUsage
	}
	if *pieceSize <= 0 {
		fmt.Fprintln(stderr, "swartznet: --piece-size must be positive")
		return exitUsage
	}
	path, prefix := fs.Arg(0), fs.Arg(1)
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: read %s: %v\n", path, err)
		return exitRuntime
	}
	tree, err := snagg.OpenBTree(snagg.BytesPageSource{Data: data, PieceSize: *pieceSize})
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: open b-tree: %v\n", err)
		return exitRuntime
	}
	if *verify {
		if err := tree.VerifyFingerprint(); err != nil {
			fmt.Fprintf(stderr, "swartznet: fingerprint verification failed: %v\n", err)
			return exitRuntime
		}
	}
	hits, err := tree.Find(prefix)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: find %q: %v\n", prefix, err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "Matches for prefix %q: %d records\n", prefix, len(hits))
	for _, h := range hits {
		fmt.Fprintf(stdout, "  %s  %-40s  t=%d\n", hex.EncodeToString(h.Ih[:]), h.Kw, h.T)
	}
	return exitOK
}
