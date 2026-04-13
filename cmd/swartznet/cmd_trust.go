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

// cmdTrust dispatches the `swartznet trust <subcommand>` family.
// The trust list is a local, offline JSON file; there is no
// daemon round-trip, so these commands work even when no
// swartznet daemon is running.
func cmdTrust(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTrustUsage(stderr)
		return exitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return trustList(rest, stdout, stderr)
	case "add":
		return trustAdd(rest, stdout, stderr)
	case "remove", "rm":
		return trustRemove(rest, stdout, stderr)
	case "help", "-h", "--help":
		printTrustUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "swartznet trust: unknown subcommand %q\n\n", sub)
		printTrustUsage(stderr)
		return exitUsage
	}
}

func printTrustUsage(w io.Writer) {
	fmt.Fprintln(w, `swartznet trust — manage the publisher trust list

Usage:
  swartznet trust list                         Print every trusted publisher.
  swartznet trust add <pubkey> [<label>]       Add (or relabel) a trusted publisher.
  swartznet trust remove <pubkey>              Remove a trusted publisher.

Flags (apply to every subcommand):
  --file <path>     Override the trust list path (default: ~/.local/share/swartznet/trust.json).
  --json            Emit JSON instead of text (list only).`)
}

func trustList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		file   string
		asJSON bool
	)
	fs.StringVar(&file, "file", "", "override the trust list path")
	fs.BoolVar(&asJSON, "json", false, "emit JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	store, err := openTrustStore(file)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	list := store.List()

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return exitOK
	}

	if len(list) == 0 {
		fmt.Fprintln(stdout, "(no trusted publishers yet — use `swartznet trust add <pubkey> <label>`)")
		return exitOK
	}
	fmt.Fprintf(stdout, "%-64s  %s\n", "PUBKEY", "LABEL")
	for _, e := range list {
		fmt.Fprintf(stdout, "%-64s  %s\n", e.PubKeyHex, e.Label)
	}
	return exitOK
}

func trustAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var file string
	fs.StringVar(&file, "file", "", "override the trust list path")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(stderr, "usage: swartznet trust add <pubkey> [<label>]")
		return exitUsage
	}
	pub := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	var label string
	if fs.NArg() >= 2 {
		label = strings.Join(fs.Args()[1:], " ")
	}

	store, err := openTrustStore(file)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	if err := store.Add(pub, label); err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintf(stdout, "added: %s", pub)
	if label != "" {
		fmt.Fprintf(stdout, " (%s)", label)
	}
	fmt.Fprintln(stdout)
	return exitOK
}

func trustRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var file string
	fs.StringVar(&file, "file", "", "override the trust list path")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: swartznet trust remove <pubkey>")
		return exitUsage
	}
	pub := strings.ToLower(strings.TrimSpace(fs.Arg(0)))

	store, err := openTrustStore(file)
	if err != nil {
		return reportRunErr(err, stderr)
	}
	if err := store.Remove(pub); err != nil {
		return reportRunErr(err, stderr)
	}
	fmt.Fprintf(stdout, "removed: %s\n", pub)
	return exitOK
}

// openTrustStore resolves the trust-list path (default config
// path if --file is empty) and returns a *trust.Store.
func openTrustStore(override string) (*trust.Store, error) {
	path := override
	if path == "" {
		path = config.Default().TrustPath
	}
	return trust.LoadOrCreate(path)
}
