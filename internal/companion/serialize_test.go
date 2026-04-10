package companion_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/companion"
)

func sampleIndex() companion.CompanionIndex {
	return companion.CompanionIndex{
		Publisher:   "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		GeneratedAt: 1712649600,
		Torrents: []companion.TorrentRecord{
			{
				InfoHash: "1111111111111111111111111111111111111111",
				Name:     "Ubuntu 24.04",
				Size:     6 << 30,
				AddedAt:  1712649000,
				Files: []companion.FileRecord{
					{
						Index:     0,
						Path:      "README.md",
						Size:      1024,
						Mime:      "text/markdown",
						Extractor: "plaintext",
						Chunks: []companion.ContentChunk{
							{Text: "the quick brown fox jumps over the lazy dog", Offset: 0},
							{Text: "second paragraph extracted from the same file", Offset: 50},
						},
					},
				},
			},
			{
				InfoHash: "2222222222222222222222222222222222222222",
				Name:     "Debian Bookworm",
				Size:     500 << 20,
			},
		},
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	orig := sampleIndex()
	encoded, err := companion.Encode(orig)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded payload is empty")
	}

	// First two bytes of a gzip stream are 0x1f 0x8b.
	if encoded[0] != 0x1f || encoded[1] != 0x8b {
		t.Errorf("encoded payload is not gzip-framed: % x", encoded[:4])
	}

	got, err := companion.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Format / Version filled in by Encode.
	if got.Format != "swartznet-content-index" {
		t.Errorf("decoded.Format = %q, want 'swartznet-content-index'", got.Format)
	}
	if got.Version != companion.FormatVersion {
		t.Errorf("decoded.Version = %d, want %d", got.Version, companion.FormatVersion)
	}

	if got.Publisher != orig.Publisher {
		t.Errorf("Publisher round-trip mismatch")
	}
	if got.GeneratedAt != orig.GeneratedAt {
		t.Errorf("GeneratedAt round-trip mismatch")
	}
	if len(got.Torrents) != 2 {
		t.Fatalf("len(Torrents) = %d, want 2", len(got.Torrents))
	}
	if got.Torrents[0].Name != "Ubuntu 24.04" {
		t.Errorf("first torrent name mismatch: %q", got.Torrents[0].Name)
	}
	if len(got.Torrents[0].Files) != 1 {
		t.Fatalf("first torrent has %d files, want 1", len(got.Torrents[0].Files))
	}
	if len(got.Torrents[0].Files[0].Chunks) != 2 {
		t.Errorf("first file has %d chunks, want 2", len(got.Torrents[0].Files[0].Chunks))
	}
	if got.Torrents[0].Files[0].Chunks[0].Text != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("chunk text round-trip failed")
	}

	// The second torrent has no files; should round-trip as
	// an empty Files slice (or nil — the JSON omitempty makes
	// either acceptable on decode).
	if len(got.Torrents[1].Files) != 0 {
		t.Errorf("second torrent should have no files, got %d", len(got.Torrents[1].Files))
	}
}

func TestEncodeFillsFormatAndVersion(t *testing.T) {
	t.Parallel()
	// Caller passes Format and Version as zero values; Encode
	// fills them in.
	idx := companion.CompanionIndex{
		Publisher: "abc",
		Torrents:  []companion.TorrentRecord{{InfoHash: "1111111111111111111111111111111111111111"}},
	}
	encoded, err := companion.Encode(idx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := companion.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != companion.FormatVersion {
		t.Errorf("Version not filled by Encode: got %d", got.Version)
	}
	if got.Format != "swartznet-content-index" {
		t.Errorf("Format not filled by Encode: got %q", got.Format)
	}
}

func TestDecodeRejectsBadFormat(t *testing.T) {
	t.Parallel()
	// Encode normally then mangle the JSON before re-gzipping.
	bad := []byte(`{"version":1,"format":"some-other-format","torrents":[]}`)
	var buf bytes.Buffer
	if err := writeGzip(&buf, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := companion.Decode(&buf); err == nil {
		t.Error("expected error for wrong format string")
	} else if !strings.Contains(err.Error(), "bad format") {
		t.Errorf("error = %q, want it to mention 'bad format'", err.Error())
	}
}

func TestDecodeRejectsFutureVersion(t *testing.T) {
	t.Parallel()
	// Encode the schema with version=99 manually so the test
	// does not depend on bumping FormatVersion in production.
	body := []byte(`{"version":99,"format":"swartznet-content-index","torrents":[]}`)
	var buf bytes.Buffer
	if err := writeGzip(&buf, body); err != nil {
		t.Fatal(err)
	}
	if _, err := companion.Decode(&buf); err == nil {
		t.Error("expected error for unsupported version")
	} else if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("error = %q, want it to mention 'unsupported version'", err.Error())
	}
}

func TestDecodeRejectsNonGzip(t *testing.T) {
	t.Parallel()
	if _, err := companion.Decode(bytes.NewReader([]byte("not gzip at all"))); err == nil {
		t.Error("expected error for non-gzip input")
	}
}

func TestDecodeRejectsBadJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := writeGzip(&buf, []byte("{not valid json")); err != nil {
		t.Fatal(err)
	}
	if _, err := companion.Decode(&buf); err == nil {
		t.Error("expected error for bad json")
	}
}

func TestEncodeIsCompactForRepetitiveContent(t *testing.T) {
	t.Parallel()
	// 1000 nearly-identical hits should compress dramatically.
	// We expect the gzipped size to be less than 5% of the
	// uncompressed JSON size — gzip is very good at this.
	const n = 1000
	idx := companion.CompanionIndex{
		Publisher: "ffff",
		Torrents:  make([]companion.TorrentRecord, n),
	}
	for i := 0; i < n; i++ {
		idx.Torrents[i] = companion.TorrentRecord{
			InfoHash: "1111111111111111111111111111111111111111",
			Name:     "Some Repeated Torrent Name",
			Size:     1 << 30,
		}
	}
	encoded, err := companion.Encode(idx)
	if err != nil {
		t.Fatal(err)
	}
	uncompressed, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	ratio := float64(len(encoded)) / float64(len(uncompressed))
	if ratio > 0.05 {
		t.Errorf("compression ratio %.3f, want < 0.05 for repetitive content (%d → %d bytes)",
			ratio, len(uncompressed), len(encoded))
	}
}

// writeGzip is a tiny test helper for synthesising bad-format
// inputs that bypass the public Encode function.
func writeGzip(w *bytes.Buffer, body []byte) error {
	// Match Encode's framing: gzip stream containing the body.
	gw, err := gzipWriterFor(w)
	if err != nil {
		return err
	}
	if _, err := gw.Write(body); err != nil {
		return err
	}
	return gw.Close()
}
