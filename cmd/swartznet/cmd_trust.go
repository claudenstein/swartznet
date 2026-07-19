package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/trust"
)

// cmdTrust manages the publisher allowlist offline — it reads and writes
// trust.json directly and never contacts a daemon.
func cmdTrust(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTrustUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "list", "ls":
		return trustList(args[1:], stdout, stderr)
	case "add":
		return trustAdd(args[1:], stdout, stderr)
	case "remove", "rm":
		return trustRemove(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printTrustUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "swartznet trust: unknown subcommand %q\n", args[0])
		printTrustUsage(stderr)
		return exitUsage
	}
}

func printTrustUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: swartznet trust <list|add|remove> [flags]")
	fmt.Fprintln(w, "  trust list [--json]                 list trusted publishers")
	fmt.Fprintln(w, "  trust add <pubkey> [<label>...]     trust a publisher (64-hex pubkey)")
	fmt.Fprintln(w, "  trust remove <pubkey>               stop trusting a publisher")
	fmt.Fprintln(w, "  --file <path>                       override the trust.json path")
}

func openTrustStore(fs *flag.FlagSet, filePath string) (*trust.Store, error) {
	path := filePath
	if path == "" {
		path = config.Default().TrustPath
	}
	return trust.LoadOrCreate(path)
}

func trustList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	filePath := fs.String("file", "", "override the trust.json path")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	store, err := openTrustStore(fs, *filePath)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	entries := store.List()
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)
		return exitOK
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "(no trusted publishers yet — use `swartznet trust add <pubkey> <label>`)")
		return exitOK
	}
	fmt.Fprintf(stdout, "%-64s  %s\n", "PUBKEY", "LABEL")
	for _, e := range entries {
		fmt.Fprintf(stdout, "%-64s  %s\n", e.PubKeyHex, e.Label)
	}
	return exitOK
}

func trustAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	filePath := fs.String("file", "", "override the trust.json path")
	// Accept --file before OR after the positional pubkey/label. Without this,
	// `trust add <pubkey> --file X` stops flag parsing at the pubkey and absorbs
	// "--file X" into the label, silently writing to the DEFAULT store.
	pos, err := parseFlagsAllowingLeadingPositionals(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) < 1 {
		fmt.Fprintln(stderr, "usage: swartznet trust add <pubkey> [<label>]")
		return exitUsage
	}
	pub := strings.ToLower(strings.TrimSpace(pos[0]))
	label := strings.Join(pos[1:], " ")
	store, err := openTrustStore(fs, *filePath)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	if err := store.Add(pub, label); err != nil {
		return reportRunErr(err, stderr)
	}
	if label != "" {
		fmt.Fprintf(stdout, "added: %s (%s)\n", pub, label)
	} else {
		fmt.Fprintf(stdout, "added: %s\n", pub)
	}
	return exitOK
}

func trustRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	filePath := fs.String("file", "", "override the trust.json path")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: swartznet trust remove <pubkey>")
		return exitUsage
	}
	pub := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	store, err := openTrustStore(fs, *filePath)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	if err := store.Remove(pub); err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintf(stdout, "removed: %s\n", pub)
	return exitOK
}
