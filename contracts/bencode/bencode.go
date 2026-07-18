// Package bencode is the raw-bytes-preserving bencode substrate of the
// contracts tier. Decoding a metainfo as map[string]bencode.Bytes captures
// the info value as its EXACT original bytes, so the infohash and (later)
// torrent signatures are always derived from bytes that never round-tripped
// through a typed struct — a typed round-trip would change the infohash.
//
// The package knows nothing of pieces, storage, or priorities; the Metainfo
// view is decode-only and must never grow a Marshal method.
package bencode

import (
	"crypto/sha1"
	"errors"
	"fmt"

	abencode "github.com/anacrolix/torrent/bencode"
)

// Bytes re-exports the raw-slice pass-through value type: it unmarshals by
// capturing the exact source bytes and marshals by emitting them verbatim.
type Bytes = abencode.Bytes

// DecodeDict decodes raw as a bencoded dictionary of raw values. Trailing
// bytes after the dictionary are an error (anacrolix Unmarshal is strict) —
// malformed files must fail at add time, not at restore time.
func DecodeDict(raw []byte) (map[string]Bytes, error) {
	var d map[string]Bytes
	if err := abencode.Unmarshal(raw, &d); err != nil {
		var trailing abencode.ErrUnusedTrailingBytes
		if errors.As(err, &trailing) {
			return nil, fmt.Errorf("bencode: trailing data after metainfo dict")
		}
		return nil, fmt.Errorf("bencode: decode metainfo: %w", err)
	}
	return d, nil
}

// EncodeDict encodes d with keys sorted (the bencode canonical order) and
// every value emitted byte-verbatim. Unknown keys survive a
// DecodeDict→EncodeDict round-trip unchanged.
func EncodeDict(d map[string]Bytes) ([]byte, error) {
	out, err := abencode.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("bencode: encode metainfo: %w", err)
	}
	return out, nil
}

// FileEntry is one file in a metainfo view. Single-file torrents yield one
// entry with an empty Path.
type FileEntry struct {
	Length int64
	Path   []string
}

// Metainfo is a decode-only view of a .torrent. InfoBytes are the exact raw
// bytes of the top-level "info" value; InfoHash is always SHA1(InfoBytes).
// The scalar fields exist for display and validation only.
type Metainfo struct {
	InfoBytes    []byte
	InfoHash     [20]byte
	Announce     string
	AnnounceList [][]string
	Name         string
	PieceLength  int64
	Files        []FileEntry
	TotalLength  int64
}

// InfoHashHex returns the infohash as 40 lowercase hex characters.
func (m *Metainfo) InfoHashHex() string {
	return fmt.Sprintf("%x", m.InfoHash)
}

// ParseMetainfo decodes raw as a .torrent, preserving the info dict bytes.
func ParseMetainfo(raw []byte) (*Metainfo, error) {
	top, err := DecodeDict(raw)
	if err != nil {
		return nil, err
	}
	infoRaw, ok := top["info"]
	if !ok || len(infoRaw) == 0 {
		return nil, fmt.Errorf("bencode: metainfo missing info dict")
	}

	m := &Metainfo{
		InfoBytes: []byte(infoRaw),
		InfoHash:  sha1.Sum(infoRaw),
	}
	if a, ok := top["announce"]; ok {
		_ = abencode.Unmarshal(a, &m.Announce)
	}
	if al, ok := top["announce-list"]; ok {
		_ = abencode.Unmarshal(al, &m.AnnounceList)
	}

	var info struct {
		Name        string `bencode:"name"`
		PieceLength int64  `bencode:"piece length"`
		Length      int64  `bencode:"length"`
		Files       []struct {
			Length int64    `bencode:"length"`
			Path   []string `bencode:"path"`
		} `bencode:"files"`
	}
	if err := abencode.Unmarshal(infoRaw, &info); err != nil {
		return nil, fmt.Errorf("bencode: decode metainfo: %w", err)
	}
	m.Name = info.Name
	m.PieceLength = info.PieceLength
	if len(info.Files) > 0 {
		for _, f := range info.Files {
			m.Files = append(m.Files, FileEntry{Length: f.Length, Path: f.Path})
			m.TotalLength += f.Length
		}
	} else {
		m.Files = []FileEntry{{Length: info.Length}}
		m.TotalLength = info.Length
	}
	return m, nil
}
