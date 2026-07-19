package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/swartznet/swartznet/contracts/record"
	"github.com/swartznet/swartznet/contracts/snagg"
	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/identity"
)

// aggregatePoWCap is the frozen hashcash difficulty ceiling; build refuses more.
const aggregatePoWCap = 40

// cmdAggregateBuild signs + packs a JSONL record set into a signed SNAGG B-tree,
// fully offline. Output file mode is 0644.
func cmdAggregateBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aggregate build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inPath := fs.String("in", "-", "JSONL input file; '-' reads from stdin")
	outPath := fs.String("out", "", "output path for the signed B-tree payload (required)")
	keyPath := fs.String("key", "", "ed25519 identity file; defaults to the node's ~/.local/share/swartznet/identity.key")
	seq := fs.Uint64("seq", 1, "sequence number to embed in the trailer (monotonic per publisher)")
	pieceSize := fs.Int("piece-size", snagg.MinPieceSize, "piece size in bytes; MUST match the .torrent's metainfo when wrapped")
	powBits := fs.Uint("pow-bits", 0, "hashcash difficulty (0 = no mining, 20 = production default)")
	if err := fs.Parse(args); err != nil {
		return parseErrExit(err)
	}
	if *outPath == "" {
		fmt.Fprintln(stderr, "aggregate build: --out is required")
		return exitUsage
	}
	if *powBits > aggregatePoWCap {
		fmt.Fprintln(stderr, "aggregate build: --pow-bits above 40 refused (cost prohibitive)")
		return exitUsage
	}

	priv, pub, err := loadAggregateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "aggregate build: load key: %v\n", err)
		return exitRuntime
	}
	recs, err := readAggregateRecords(*inPath, os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "aggregate build: read records: %v\n", err)
		return exitRuntime
	}
	if len(recs) == 0 {
		fmt.Fprintln(stderr, "aggregate build: no records in input")
		return exitUsage
	}

	built, err := buildAndSignAggregate(recs, priv, pub, *seq, *pieceSize, uint8(*powBits))
	if err != nil {
		fmt.Fprintf(stderr, "aggregate build: %v\n", err)
		return exitRuntime
	}
	if err := os.WriteFile(*outPath, built.Bytes, 0o644); err != nil {
		fmt.Fprintf(stderr, "aggregate build: write %s: %v\n", *outPath, err)
		return exitRuntime
	}

	fmt.Fprintln(stdout, "Built Aggregate index")
	fmt.Fprintf(stdout, "  records:     %d\n", built.NumRecords)
	fmt.Fprintf(stdout, "  pages:       %d\n", built.NumPages)
	fmt.Fprintf(stdout, "  bytes:       %d\n", len(built.Bytes))
	fmt.Fprintf(stdout, "  fingerprint: %s\n", hex.EncodeToString(built.Fingerprint[:]))
	fmt.Fprintf(stdout, "  output:      %s\n", *outPath)
	return exitOK
}

// loadAggregateKey resolves the signing identity. An empty path auto-creates the
// default XDG identity (the only place minting is allowed); an explicit path is
// fail-closed (never mints — a typo must not orphan records under a fresh key).
func loadAggregateKey(keyPath string) (ed25519.PrivateKey, [32]byte, error) {
	var pub [32]byte
	if keyPath == "" {
		id, err := identity.Load(config.Default().IdentityPath, true)
		if err != nil {
			return nil, pub, err
		}
		return id.PrivateKey, id.PublicKeyBytes(), nil
	}
	if _, err := os.Stat(keyPath); err != nil {
		return nil, pub, fmt.Errorf("identity file %q: %w", keyPath, err)
	}
	id, err := identity.Load(keyPath, false)
	if err != nil {
		return nil, pub, err
	}
	return id.PrivateKey, id.PublicKeyBytes(), nil
}

type jsonRecord struct {
	Kw string `json:"kw"`
	IH string `json:"ih"`
	T  int64  `json:"t"`
}

// readAggregateRecords parses the JSONL input (one {kw,ih,t} per line).
func readAggregateRecords(inPath string, in io.Reader) ([]jsonRecord, error) {
	var r io.Reader
	if inPath == "-" {
		r = in
	} else {
		f, err := os.Open(inPath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var out []jsonRecord
	line := 0
	for sc.Scan() {
		line++
		b := sc.Bytes()
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
		out = append(out, jr)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildAndSignAggregate(recs []jsonRecord, priv ed25519.PrivateKey, pub [32]byte, seq uint64, pieceSize int, powBits uint8) (snagg.BuiltTree, error) {
	signed := make([]snagg.Record, 0, len(recs))
	for i, jr := range recs {
		raw, err := hex.DecodeString(jr.IH)
		if err != nil {
			return snagg.BuiltTree{}, fmt.Errorf("record %d: decode ih: %w", i, err)
		}
		if len(raw) != 20 {
			return snagg.BuiltTree{}, fmt.Errorf("record %d: ih decoded to %d bytes", i, len(raw))
		}
		var ih [20]byte
		copy(ih[:], raw)
		r, err := record.SignAndMine(priv, pub, jr.Kw, ih, jr.T, int(powBits))
		if err != nil {
			return snagg.BuiltTree{}, fmt.Errorf("record %d: sign+mine: %w", i, err)
		}
		signed = append(signed, r)
	}
	return snagg.BuildBTree(snagg.BuildInput{
		Records: signed, PubKey: pub, PrivKey: priv, Seq: seq, PieceSize: pieceSize, MinPoWBits: powBits,
	})
}
