package companion

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Encode serialises a CompanionIndex into the on-the-wire form:
// JSON inside a single gzip stream. The caller is responsible
// for writing the result to disk (or wrapping it in a torrent).
//
// The Format and Version fields are filled in by the encoder so
// the caller doesn't have to remember to set them. Any value
// the caller passes in for those two fields is overwritten.
func Encode(idx CompanionIndex) ([]byte, error) {
	idx.Format = "swartznet-content-index"
	idx.Version = FormatVersion
	if idx.Torrents == nil {
		idx.Torrents = []TorrentRecord{}
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	if err := enc.Encode(idx); err != nil {
		return nil, fmt.Errorf("companion: encode json: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("companion: close gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode reverses Encode. It reads the gzip stream, decodes the
// JSON, and validates the Format and Version fields. Subscribers
// MUST call Decode (rather than directly unmarshaling) so the
// version check is enforced consistently across the codebase.
func Decode(r io.Reader) (CompanionIndex, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return CompanionIndex{}, fmt.Errorf("companion: open gzip: %w", err)
	}
	defer gz.Close()

	// Read up to a generous cap to avoid a malicious publisher
	// sending an unbounded stream that exhausts our memory. 1
	// GiB of decompressed JSON is enough for any reasonable
	// content index — that's roughly the M2 chunker output of a
	// terabyte of indexed text.
	const maxDecompressed = 1 << 30
	body, err := io.ReadAll(io.LimitReader(gz, maxDecompressed+1))
	if err != nil {
		return CompanionIndex{}, fmt.Errorf("companion: read gzip: %w", err)
	}
	if int64(len(body)) > maxDecompressed {
		return CompanionIndex{}, errors.New("companion: decompressed payload exceeds 1 GiB safety cap")
	}

	var out CompanionIndex
	if err := json.Unmarshal(body, &out); err != nil {
		return CompanionIndex{}, fmt.Errorf("companion: parse json: %w", err)
	}
	if out.Format != "swartznet-content-index" {
		return CompanionIndex{}, fmt.Errorf("companion: bad format %q, want 'swartznet-content-index'", out.Format)
	}
	if out.Version != FormatVersion {
		return CompanionIndex{}, fmt.Errorf("companion: unsupported version %d, this build understands %d",
			out.Version, FormatVersion)
	}
	if out.Torrents == nil {
		out.Torrents = []TorrentRecord{}
	}
	return out, nil
}

// EncodeSize is a small helper that returns just the encoded
// size without producing the full byte slice. Useful for
// publisher-side budgeting before committing to a put.
func EncodeSize(idx CompanionIndex) (int, error) {
	buf, err := Encode(idx)
	if err != nil {
		return 0, err
	}
	return len(buf), nil
}
