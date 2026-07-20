package companion

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// maxDecompressed bounds the decompressed companion payload — defence in depth
// against a gzip bomb from an untrusted publisher (the BEP-46 pointer resolves
// to an attacker-chosen infohash). 1 GiB is far above any legitimate index yet
// well below memory exhaustion.
const maxDecompressed = 1 << 30

// Encode serialises a CompanionIndex to gzip(JSON). It force-sets Format and
// Version (caller values are overwritten by design) and normalises a nil
// Torrents to the empty slice so the JSON carries "torrents":[] never
// "torrents":null — subscribers rely on this.
func Encode(idx CompanionIndex) ([]byte, error) {
	idx.Format = FormatName
	idx.Version = FormatVersion
	if idx.Torrents == nil {
		idx.Torrents = []TorrentRecord{}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(idx); err != nil {
		_ = gz.Close()
		return nil, fmt.Errorf("companion: encode json: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("companion: close gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode parses a gzip(JSON) companion payload retrieved from an untrusted
// publisher. It bounds the decompressed size, then refuses an unknown format or
// version BEFORE trusting the records. A nil Torrents is normalised to empty.
func Decode(r io.Reader) (CompanionIndex, error) {
	var out CompanionIndex
	gz, err := gzip.NewReader(r)
	if err != nil {
		return out, fmt.Errorf("companion: open gzip: %w", err)
	}
	defer gz.Close()
	raw, err := io.ReadAll(io.LimitReader(gz, maxDecompressed+1))
	if err != nil {
		return out, fmt.Errorf("companion: read gzip: %w", err)
	}
	if int64(len(raw)) > maxDecompressed {
		return out, errors.New("companion: decompressed payload exceeds 1 GiB safety cap")
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("companion: parse json: %w", err)
	}
	if out.Format != FormatName {
		return out, fmt.Errorf("companion: bad format %q, want %q", out.Format, FormatName)
	}
	if out.Version != FormatVersion {
		return out, fmt.Errorf("companion: unsupported version %d, this build understands %d", out.Version, FormatVersion)
	}
	if out.Torrents == nil {
		out.Torrents = []TorrentRecord{}
	}
	return out, nil
}
