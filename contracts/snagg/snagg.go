// Package snagg is the frozen byte format of the signed SNAGG B-tree Aggregate
// index — a page-structured, ed25519-signed, prefix-queryable companion index
// whose pages are exactly one torrent piece wide. It is a cross-implementation
// wire contract: the 6-byte magic, the 16-byte page header, the bencoded record
// (key order ih<kw<pk<pow<sig<t), the 162-byte signed trailer, the MIN-KEY
// separator scheme, and the SHA-256 record-stream fingerprint must be
// reproduced byte-for-byte. Golden vectors in the test pin the bytes.
//
// The record TYPE + its sign/PoW preimage are reused from contracts/record
// (SigMessage = Pk||kw||Ih||LE64(T)||uvarint(Pow)); snagg adds only the bencode
// wire form, the page/tree structure, and the trailer. Record identity
// (ElementID, excluding pow+sig) and the SNAGG fingerprint (over the FULL
// bencoded record, including pow+sig) are deliberately distinct preimages —
// never unify them.
package snagg

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/contracts/record"
)

// Frozen format constants.
var BTreeMagic = [6]byte{'S', 'N', 'A', 'G', 'G', 0x00}

const (
	BTreeVersion      = 0x01 // page-header version byte
	PageKindRoot      = 0x00
	PageKindInterior  = 0x01
	PageKindLeaf      = 0x02
	PageKindTrailer   = 0xFF
	PageHeaderSize    = 16
	MinPoWBitsDefault = 20 // default hashcash difficulty (NOT the 40 cap)
	MaxKeywordBytes   = 64
	MaxRecordBytes    = 256 // hard ceiling on one bencoded record
	// TrailerPayloadSize = 1+32+8+8+4+4+8+1+32+64.
	TrailerPayloadSize = 162
	TrailerVersion     = 0x01
	MinPieceSize       = 16384
	MaxPieceSize       = 4 * 1024 * 1024
	// trailerSignedPrefix is the trailer payload minus the 64-byte signature.
	trailerSignedPrefix = TrailerPayloadSize - 64 // 98
)

// Record is the SNAGG record — reused verbatim from contracts/record so the
// sign/PoW preimage and identity stay unified across the Aggregate substrate.
type Record = record.Record

// ErrPageOverflow signals that a page's header+payload exceeds pieceSize — the
// builder's leaf/interior split signal.
var ErrPageOverflow = errors.New("snagg: page overflow")

// pageHeader is the fixed 16-byte page header.
type pageHeader struct {
	kind    uint8
	level   uint8
	payload uint16
}

func encodeHeader(dst []byte, h pageHeader) {
	copy(dst[0:6], BTreeMagic[:])
	dst[6] = BTreeVersion
	dst[7] = h.kind
	dst[8] = h.level
	dst[9] = 0 // flags
	binary.LittleEndian.PutUint16(dst[10:12], h.payload)
	// dst[12:16] reserved, left zero.
}

func decodeHeader(page []byte) (pageHeader, error) {
	if len(page) < PageHeaderSize {
		return pageHeader{}, fmt.Errorf("snagg: page %d bytes, need %d for header", len(page), PageHeaderSize)
	}
	if !bytes.Equal(page[0:6], BTreeMagic[:]) {
		return pageHeader{}, fmt.Errorf("snagg: bad page magic %q, want %q", page[0:6], BTreeMagic[:])
	}
	if page[6] != BTreeVersion {
		return pageHeader{}, fmt.Errorf("snagg: page version %d unsupported (this build reads %d)", page[6], BTreeVersion)
	}
	return pageHeader{kind: page[7], level: page[8], payload: binary.LittleEndian.Uint16(page[10:12])}, nil
}

// recordWire is the bencoded transport form. anacrolix bencode sorts struct
// fields by tag, so keys emit in lexicographic order: ih < kw < pk < pow < sig < t.
type recordWire struct {
	IH  []byte `bencode:"ih"`
	Kw  string `bencode:"kw"`
	Pk  []byte `bencode:"pk"`
	Pow uint64 `bencode:"pow"`
	Sig []byte `bencode:"sig"`
	T   int64  `bencode:"t"`
}

// EncodeRecord bencodes r into its canonical wire form (≤ MaxRecordBytes).
func EncodeRecord(r Record) ([]byte, error) {
	if len(r.Kw) == 0 {
		return nil, errors.New("snagg: record keyword is empty")
	}
	if len(r.Kw) > MaxKeywordBytes {
		return nil, fmt.Errorf("snagg: record keyword %d bytes exceeds cap %d", len(r.Kw), MaxKeywordBytes)
	}
	out, err := bencode.Marshal(recordWire{IH: r.Ih[:], Kw: r.Kw, Pk: r.Pk[:], Pow: r.Pow, Sig: r.Sig[:], T: r.T})
	if err != nil {
		return nil, fmt.Errorf("snagg: marshal record: %w", err)
	}
	if len(out) > MaxRecordBytes {
		return nil, fmt.Errorf("snagg: encoded record %d bytes exceeds cap %d", len(out), MaxRecordBytes)
	}
	return out, nil
}

// DecodeRecord parses a bencoded record. It validates field widths but does NOT
// verify the signature or PoW (transport form only).
func DecodeRecord(b []byte) (Record, error) {
	var w recordWire
	// Bound the decode: leaf pages are structurally unauthenticated (Find does not
	// hash them), so a hostile record blob could declare an inner string length
	// near the anacrolix ~128 MiB MaxStrLen default and force that allocation
	// before the field-width checks below ever run (make([]byte, declaredLen)
	// happens inside Unmarshal). A bencoded string can't exceed the blob that
	// holds it, so MaxStrLen = len(b) rejects only impossible/hostile lengths —
	// the same alloc-amplification defense as contracts/dhtschema + ltepwire.
	d := bencode.NewDecoder(bytes.NewReader(b))
	if len(b) > 0 {
		d.MaxStrLen = int64(len(b))
	}
	if err := d.Decode(&w); err != nil {
		return Record{}, fmt.Errorf("snagg: unmarshal record: %w", err)
	}
	if len(w.Pk) != 32 {
		return Record{}, fmt.Errorf("snagg: record pk %d bytes, want 32", len(w.Pk))
	}
	if len(w.IH) != 20 {
		return Record{}, fmt.Errorf("snagg: record ih %d bytes, want 20", len(w.IH))
	}
	if len(w.Sig) != 64 {
		return Record{}, fmt.Errorf("snagg: record sig %d bytes, want 64", len(w.Sig))
	}
	if len(w.Kw) > MaxKeywordBytes {
		return Record{}, fmt.Errorf("snagg: record keyword %d bytes exceeds cap %d", len(w.Kw), MaxKeywordBytes)
	}
	var r Record
	copy(r.Pk[:], w.Pk)
	copy(r.Ih[:], w.IH)
	copy(r.Sig[:], w.Sig)
	r.Kw = w.Kw
	r.Pow = w.Pow
	r.T = w.T
	return r, nil
}

// RecordKey is the leaf sort key + interior separator source: kw || 0x00 ||
// ih[20]. The 0x00 groups a keyword's records contiguously, tie-broken by ih.
func RecordKey(r Record) []byte {
	key := make([]byte, 0, len(r.Kw)+1+20)
	key = append(key, r.Kw...)
	key = append(key, 0x00)
	key = append(key, r.Ih[:]...)
	return key
}

func compareRecords(a, b Record) int { return bytes.Compare(RecordKey(a), RecordKey(b)) }

// Trailer is the 162-byte signed footer (page kind 0xFF, last piece).
type Trailer struct {
	Version        uint8
	PubKey         [32]byte
	Seq            uint64
	CreatedTs      uint64
	RootPieceIndex uint32 // invariant 0
	NumPages       uint32 // includes the trailer page
	NumRecords     uint64
	MinPoWBits     uint8
	Fingerprint    [32]byte
	PublisherSig   [64]byte
}

// encodeTrailerFields writes the first 98 bytes (everything except the
// signature) — the ed25519 sign preimage.
func encodeTrailerFields(t Trailer) []byte {
	b := make([]byte, trailerSignedPrefix)
	b[0] = t.Version
	copy(b[1:33], t.PubKey[:])
	binary.LittleEndian.PutUint64(b[33:41], t.Seq)
	binary.LittleEndian.PutUint64(b[41:49], t.CreatedTs)
	binary.LittleEndian.PutUint32(b[49:53], t.RootPieceIndex)
	binary.LittleEndian.PutUint32(b[53:57], t.NumPages)
	binary.LittleEndian.PutUint64(b[57:65], t.NumRecords)
	b[65] = t.MinPoWBits
	copy(b[66:98], t.Fingerprint[:])
	return b
}

// TrailerSigMessage is the ed25519 sign preimage (the 98 pre-signature bytes).
func TrailerSigMessage(t Trailer) []byte { return encodeTrailerFields(t) }

// SignTrailer sets PublisherSig = ed25519 over TrailerSigMessage.
func SignTrailer(t *Trailer, priv ed25519.PrivateKey) {
	sig := ed25519.Sign(priv, TrailerSigMessage(*t))
	copy(t.PublisherSig[:], sig)
}

// VerifyTrailerSig checks the trailer signature against its embedded pubkey.
func VerifyTrailerSig(t Trailer) error {
	if !ed25519.Verify(ed25519.PublicKey(t.PubKey[:]), TrailerSigMessage(t), t.PublisherSig[:]) {
		return errors.New("snagg: trailer signature failed to verify")
	}
	return nil
}

// EncodeTrailer writes a full trailer page zero-padded to pageSize.
func EncodeTrailer(t Trailer, pageSize int) ([]byte, error) {
	if pageSize < PageHeaderSize+TrailerPayloadSize {
		return nil, fmt.Errorf("snagg: page %d bytes too small for trailer (needs %d)", pageSize, PageHeaderSize+TrailerPayloadSize)
	}
	if t.Version != TrailerVersion {
		return nil, fmt.Errorf("snagg: unsupported trailer version %d", t.Version)
	}
	page := make([]byte, pageSize)
	encodeHeader(page, pageHeader{kind: PageKindTrailer, level: 0, payload: TrailerPayloadSize})
	p := page[PageHeaderSize:]
	copy(p, encodeTrailerFields(t))
	copy(p[trailerSignedPrefix:TrailerPayloadSize], t.PublisherSig[:])
	return page, nil
}

// DecodeTrailer parses a trailer page (does NOT verify the signature).
func DecodeTrailer(page []byte) (Trailer, error) {
	h, err := decodeHeader(page)
	if err != nil {
		return Trailer{}, err
	}
	if h.kind != PageKindTrailer {
		return Trailer{}, fmt.Errorf("snagg: expected trailer, got kind 0x%02x", h.kind)
	}
	if int(h.payload) != TrailerPayloadSize {
		return Trailer{}, fmt.Errorf("snagg: trailer payload length %d, expected %d", h.payload, TrailerPayloadSize)
	}
	// h.payload is read from the header bytes and does not bound the actual page
	// length; a crafted short page could declare payload=162 while being <178
	// bytes, so guard the slice like DecodeLeaf/DecodeInterior do (else the next
	// line panics on malformed input instead of erroring).
	if PageHeaderSize+TrailerPayloadSize > len(page) {
		return Trailer{}, fmt.Errorf("snagg: trailer payload length exceeds page")
	}
	p := page[PageHeaderSize : PageHeaderSize+TrailerPayloadSize]
	if p[0] != TrailerVersion {
		return Trailer{}, fmt.Errorf("snagg: unsupported trailer version %d", p[0])
	}
	var t Trailer
	t.Version = p[0]
	copy(t.PubKey[:], p[1:33])
	t.Seq = binary.LittleEndian.Uint64(p[33:41])
	t.CreatedTs = binary.LittleEndian.Uint64(p[41:49])
	t.RootPieceIndex = binary.LittleEndian.Uint32(p[49:53])
	t.NumPages = binary.LittleEndian.Uint32(p[53:57])
	t.NumRecords = binary.LittleEndian.Uint64(p[57:65])
	t.MinPoWBits = p[65]
	copy(t.Fingerprint[:], p[66:98])
	copy(t.PublisherSig[:], p[98:162])
	return t, nil
}

// Fingerprint = SHA256(concat EncodeRecord over RecordKey-sorted records). It
// hashes the FULL bencoded record (pow+sig included); it is the PPMI commit and
// the cross-impl anchor. Records must already be in sorted order.
func fingerprintSorted(sorted []Record) ([32]byte, error) {
	h := sha256.New()
	for _, r := range sorted {
		enc, err := EncodeRecord(r)
		if err != nil {
			return [32]byte{}, err
		}
		h.Write(enc)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}
