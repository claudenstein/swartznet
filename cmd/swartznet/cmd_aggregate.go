package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/swartznet/swartznet/internal/companion"
)

// cmdAggregate dispatches the `swartznet aggregate <subcommand>`
// tree. These subcommands are ops tooling for the v0.5.0
// "Aggregate" redesign — they exercise the companion-index
// B-tree path without needing a running daemon. Useful for
// operators validating a locally-built index before publishing
// and for subscribers inspecting index torrents they've
// downloaded.
func cmdAggregate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "swartznet aggregate: missing subcommand")
		printAggregateUsage(stderr)
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "inspect":
		return cmdAggregateInspect(rest, stdout, stderr)
	case "find":
		return cmdAggregateFind(rest, stdout, stderr)
	case "help", "-h", "--help":
		printAggregateUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "swartznet aggregate: unknown subcommand %q\n\n", sub)
		printAggregateUsage(stderr)
		return exitUsage
	}
}

func printAggregateUsage(w io.Writer) {
	fmt.Fprintln(w, `swartznet aggregate — Aggregate (PPMI + B-tree) ops tooling

Usage:
  swartznet aggregate <subcommand> [args]

Subcommands:
  inspect <index-file>              Print trailer metadata for an Aggregate index.
  find <index-file> <prefix>        List records matching a keyword prefix.
  help                              Print this message.

The <index-file> path points at the single-file payload inside an
Aggregate companion torrent (i.e. the bytes BuildBTree produces).
Operators get this file by either building it locally via engine
APIs or extracting it from a downloaded companion torrent via
the engine's "files" listing.`)
}

// cmdAggregateInspect prints the trailer + high-level stats for
// an Aggregate index file. Fails if the file isn't a well-formed
// B-tree (bad magic, wrong version, bad trailer signature, etc.).
func cmdAggregateInspect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aggregate inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var pieceSize int
	fs.IntVar(&pieceSize, "piece-size", companion.MinPieceSize,
		"piece size in bytes; must match the torrent's metainfo")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: swartznet aggregate inspect <index-file>")
		return exitUsage
	}
	path := fs.Arg(0)

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: read %s: %v\n", path, err)
		return exitRuntime
	}
	src := &companion.BytesPageSource{Data: data, PieceSize: pieceSize}
	reader, err := companion.OpenBTree(src)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: open b-tree: %v\n", err)
		return exitRuntime
	}
	tr := reader.Trailer()

	fmt.Fprintln(stdout, "Aggregate index inspection")
	fmt.Fprintf(stdout, "  file:           %s\n", path)
	fmt.Fprintf(stdout, "  file size:      %d bytes\n", len(data))
	fmt.Fprintf(stdout, "  piece size:     %d bytes\n", pieceSize)
	fmt.Fprintf(stdout, "  pages:          %d\n", tr.NumPages)
	fmt.Fprintf(stdout, "  records:        %d\n", tr.NumRecords)
	fmt.Fprintf(stdout, "  publisher pk:   %s\n", hex.EncodeToString(tr.PubKey[:]))
	fmt.Fprintf(stdout, "  sequence:       %d\n", tr.Seq)
	fmt.Fprintf(stdout, "  created:        %d (unix)\n", tr.CreatedTs)
	fmt.Fprintf(stdout, "  min PoW bits:   %d\n", tr.MinPoWBits)
	fmt.Fprintf(stdout, "  fingerprint:    %s\n", hex.EncodeToString(tr.TreeFingerprint[:]))
	return exitOK
}

// cmdAggregateFind runs a prefix-query and prints the matching
// records' infohash + keyword. Mirrors what a subscriber would
// get back from BTreeReader.Find.
func cmdAggregateFind(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aggregate find", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var pieceSize int
	var verify bool
	fs.IntVar(&pieceSize, "piece-size", companion.MinPieceSize,
		"piece size in bytes; must match the torrent's metainfo")
	fs.BoolVar(&verify, "verify", false,
		"also run VerifyFingerprint (scans every leaf; slower)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: swartznet aggregate find [--piece-size=N] [--verify] <index-file> <prefix>")
		return exitUsage
	}
	path, prefix := fs.Arg(0), fs.Arg(1)

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: read %s: %v\n", path, err)
		return exitRuntime
	}
	src := &companion.BytesPageSource{Data: data, PieceSize: pieceSize}
	reader, err := companion.OpenBTree(src)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: open b-tree: %v\n", err)
		return exitRuntime
	}
	if verify {
		if err := reader.VerifyFingerprint(); err != nil {
			fmt.Fprintf(stderr, "swartznet: fingerprint verification failed: %v\n", err)
			return exitRuntime
		}
	}
	hits, err := reader.Find(prefix)
	if err != nil {
		fmt.Fprintf(stderr, "swartznet: find %q: %v\n", prefix, err)
		return exitRuntime
	}

	fmt.Fprintf(stdout, "Matches for prefix %q: %d records\n", prefix, len(hits))
	for _, h := range hits {
		fmt.Fprintf(stdout, "  %s  %-40s  t=%d\n",
			hex.EncodeToString(h.Ih[:]),
			h.Kw,
			h.T)
	}
	return exitOK
}
