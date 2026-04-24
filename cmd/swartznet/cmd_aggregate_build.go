package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/swartznet/swartznet/internal/companion"
	"github.com/swartznet/swartznet/internal/identity"
)

// jsonRecord is the JSONL input schema for `aggregate build`.
// Users produce this from their own extraction pipelines; we
// sign + (optionally) mine PoW + pack into the B-tree.
type jsonRecord struct {
	Kw string `json:"kw"`
	IH string `json:"ih"` // 40-char lowercase hex SHA-1 infohash
	T  int64  `json:"t"`  // unix timestamp; optional, defaults to 0
}

// cmdAggregateBuild reads a JSONL file of records, signs them
// with the publisher's ed25519 key, optionally mines PoW, then
// writes an Aggregate B-tree index file ready for `inspect` and
// `find` to consume.
//
// The subcommand is deliberately offline — it never touches the
// DHT, a running daemon, or the network. Pair it with a separate
// "push to DHT" step once the engine integration lands; for
// today, use `aggregate inspect` on the output to confirm the
// signed record count matches your input.
func cmdAggregateBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aggregate build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		inPath     string
		outPath    string
		keyPath    string
		seq        uint64
		pieceSize  int
		powBits    uint
	)
	fs.StringVar(&inPath, "in", "-",
		"JSONL input file; '-' reads from stdin")
	fs.StringVar(&outPath, "out", "",
		"output path for the signed B-tree payload (required)")
	fs.StringVar(&keyPath, "key", "",
		"ed25519 identity file; defaults to the node's ~/.local/share/swartznet/identity.key")
	fs.Uint64Var(&seq, "seq", 1,
		"sequence number to embed in the trailer (monotonic per publisher)")
	fs.IntVar(&pieceSize, "piece-size", companion.MinPieceSize,
		"piece size in bytes; MUST match the .torrent's metainfo when wrapped")
	fs.UintVar(&powBits, "pow-bits", 0,
		"hashcash difficulty (0 = no mining, 20 = production default)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if outPath == "" {
		fmt.Fprintln(stderr, "aggregate build: --out is required")
		return exitUsage
	}
	if powBits > 40 {
		fmt.Fprintln(stderr, "aggregate build: --pow-bits above 40 refused (cost prohibitive)")
		return exitUsage
	}

	priv, pub, err := loadPrivKey(keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "aggregate build: load key: %v\n", err)
		return exitRuntime
	}
	var pk [32]byte
	copy(pk[:], pub)

	recs, err := readRecords(inPath)
	if err != nil {
		fmt.Fprintf(stderr, "aggregate build: read records: %v\n", err)
		return exitRuntime
	}
	if len(recs) == 0 {
		fmt.Fprintln(stderr, "aggregate build: no records in input")
		return exitUsage
	}

	built, err := buildAndSign(recs, pub, priv, pk, seq, pieceSize, uint8(powBits))
	if err != nil {
		fmt.Fprintf(stderr, "aggregate build: %v\n", err)
		return exitRuntime
	}

	if err := os.WriteFile(outPath, built.Bytes, 0644); err != nil {
		fmt.Fprintf(stderr, "aggregate build: write %s: %v\n", outPath, err)
		return exitRuntime
	}
	fmt.Fprintf(stdout, "Built Aggregate index\n")
	fmt.Fprintf(stdout, "  records:     %d\n", built.NumRecords)
	fmt.Fprintf(stdout, "  pages:       %d\n", built.NumPages)
	fmt.Fprintf(stdout, "  bytes:       %d\n", len(built.Bytes))
	fmt.Fprintf(stdout, "  fingerprint: %s\n", hex.EncodeToString(built.TreeFingerprint[:]))
	fmt.Fprintf(stdout, "  output:      %s\n", outPath)
	return exitOK
}

// loadPrivKey loads the publisher's ed25519 keypair. An explicit
// path overrides the default identity location; passing "" uses
// identity.LoadOrCreate with the default XDG path.
func loadPrivKey(keyPath string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if keyPath == "" {
		path, err := defaultIdentityPath()
		if err != nil {
			return nil, nil, err
		}
		id, err := identity.LoadOrCreate(path)
		if err != nil {
			return nil, nil, err
		}
		return id.PrivateKey, id.PublicKey, nil
	}
	// Explicit path: load only, don't auto-generate.
	id, err := identity.LoadOrCreate(keyPath)
	if err != nil {
		return nil, nil, err
	}
	return id.PrivateKey, id.PublicKey, nil
}

// defaultIdentityPath mirrors the XDG path the daemon uses. We
// avoid importing internal/config just for this one constant —
// keep the default co-located with the CLI so it's obvious.
func defaultIdentityPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + "/.local/share/swartznet/identity.key", nil
}

// readRecords parses JSONL from path (or stdin when path == "-")
// and returns the decoded records. Bad lines surface as errors
// with their line number so users can fix their input quickly.
func readRecords(path string) ([]jsonRecord, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}

	scanner := bufio.NewScanner(r)
	// Default buffer is 64 KiB; bump to 1 MiB for long records.
	scanner.Buffer(make([]byte, 1<<16), 1<<20)

	var recs []jsonRecord
	line := 0
	for scanner.Scan() {
		line++
		b := scanner.Bytes()
		if len(b) == 0 {
			continue
		}
		var jr jsonRecord
		if err := json.Unmarshal(b, &jr); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if len(jr.IH) != 40 {
			return nil, fmt.Errorf("line %d: ih %d chars, want 40 (hex sha-1)", line, len(jr.IH))
		}
		if jr.Kw == "" {
			return nil, fmt.Errorf("line %d: empty kw", line)
		}
		recs = append(recs, jr)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return recs, nil
}

// buildAndSign converts JSONL records into companion.Records
// (signing + optional PoW) and hands them to BuildBTree.
func buildAndSign(raw []jsonRecord, pub ed25519.PublicKey, priv ed25519.PrivateKey, pk [32]byte, seq uint64, pieceSize int, powBits uint8) (companion.BuildBTreeOutput, error) {
	signed := make([]companion.Record, 0, len(raw))
	for i, jr := range raw {
		ihBytes, err := hex.DecodeString(jr.IH)
		if err != nil {
			return companion.BuildBTreeOutput{}, fmt.Errorf("record %d: decode ih: %w", i, err)
		}
		if len(ihBytes) != 20 {
			return companion.BuildBTreeOutput{}, fmt.Errorf("record %d: ih decoded to %d bytes", i, len(ihBytes))
		}
		var ih [20]byte
		copy(ih[:], ihBytes)

		if powBits > 0 {
			r, err := companion.SignAndMineRecord(priv, pub, jr.Kw, ih, jr.T, powBits)
			if err != nil {
				return companion.BuildBTreeOutput{}, fmt.Errorf("record %d: sign+mine: %w", i, err)
			}
			signed = append(signed, r)
			continue
		}
		// No PoW: just sign.
		var r companion.Record
		copy(r.Pk[:], pub)
		r.Kw = jr.Kw
		r.Ih = ih
		r.T = jr.T
		sig := ed25519.Sign(priv, companion.RecordSigMessage(r))
		copy(r.Sig[:], sig)
		signed = append(signed, r)
	}

	return companion.BuildBTree(companion.BuildBTreeInput{
		Records:    signed,
		PubKey:     pk,
		PrivKey:    priv,
		Seq:        seq,
		PieceSize:  pieceSize,
		MinPoWBits: powBits,
	})
}

// ErrNoRecords is returned when the JSONL input had no usable
// records after parsing.
var ErrNoRecords = errors.New("aggregate build: no records")
