// Companion B-tree page format for the "Aggregate" redesign.
// See docs/research/SPEC.md §1 for the normative specification
// and docs/research/PROPOSAL.md for the rationale.
//
// This file implements the byte-level page encode/decode only.
// Building whole trees (layOutLeaves + buildInteriorLevel +
// trailer + torrent wrapping) lives in build.go; the prefix-
// query walker lives in subscriber.go.

package companion

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/anacrolix/torrent/bencode"
)

// BTreeMagic is the leading six bytes of every B-tree page.
// Chosen so `file(1)` identifies the payload and so readers can
// fail fast on corrupt or mismatched-format pages.
var BTreeMagic = [6]byte{'S', 'N', 'A', 'G', 'G', 0x00}

// BTreeVersion is the schema version this package writes and reads.
// Readers MUST refuse pages whose version field differs.
const BTreeVersion uint8 = 0x01

// PageKind discriminates the payload schema. Values are stable on
// the wire; new kinds must allocate the next unused byte.
type PageKind uint8

const (
	PageKindRoot     PageKind = 0x00
	PageKindInterior PageKind = 0x01
	PageKindLeaf     PageKind = 0x02
	PageKindTrailer  PageKind = 0xFF
)

// PageHeaderSize is the fixed length of every page header in bytes.
const PageHeaderSize = 16

// MinPoWBitsDefault is the hashcash difficulty the v1.1 writer
// emits and the v1.1 reader enforces. Kept as a named constant so
// bumping difficulty in a later release is a one-line edit.
const MinPoWBitsDefault uint8 = 20

// MaxKeywordBytes caps the keyword portion of a record so that a
// single record's key (keyword || 0x00 || infohash) stays well
// under a page boundary.
const MaxKeywordBytes = 64

// MaxRecordBytes is the hard ceiling for one bencoded Record.
// Publishers reject oversized records at build time.
const MaxRecordBytes = 256

// TrailerPayloadSize is the fixed length of the trailer page's
// payload, derived from the field list in SPEC §1.6.
//
//	1 (trailer_version)
//	+ 32 (pubkey)
//	+ 8 (seq)
//	+ 8 (created_ts)
//	+ 4 (root_piece_index)
//	+ 4 (num_pages)
//	+ 8 (num_records)
//	+ 1 (min_pow_bits)
//	+ 32 (tree_fingerprint)
//	+ 64 (publisher_sig)
const TrailerPayloadSize = 1 + 32 + 8 + 8 + 4 + 4 + 8 + 1 + 32 + 64

// Record is one signed keyword → infohash entry. Its bencoded
// form is the payload each leaf page carries.
type Record struct {
	Pk  [32]byte // publisher ed25519 pubkey
	Kw  string   // lowercased UTF-8 keyword, ≤ MaxKeywordBytes bytes
	Ih  [20]byte // BEP-3 infohash
	T   int64    // unix timestamp at publish
	Pow uint64   // hashcash nonce; validator recomputes SHA256 bits
	Sig [64]byte // ed25519 over Pk || Kw || Ih || T || Pow
}

// recordWire mirrors Record for bencode round-trip. We keep field
// names short ("pk", "kw", "ih", "t", "pow", "sig") because every
// record in a page pays this tax.
type recordWire struct {
	Pk  []byte `bencode:"pk"`
	Kw  string `bencode:"kw"`
	Ih  []byte `bencode:"ih"`
	T   int64  `bencode:"t"`
	Pow uint64 `bencode:"pow"`
	Sig []byte `bencode:"sig"`
}

// EncodeRecord produces the canonical bencoded bytes for a
// Record. Used by the leaf encoder and by the per-record signing
// path — two callers that MUST agree on canonical form.
func EncodeRecord(r Record) ([]byte, error) {
	if len(r.Kw) == 0 {
		return nil, errors.New("companion: record keyword is empty")
	}
	if len(r.Kw) > MaxKeywordBytes {
		return nil, fmt.Errorf("companion: record keyword %d bytes exceeds cap %d",
			len(r.Kw), MaxKeywordBytes)
	}
	wire := recordWire{
		Pk:  r.Pk[:],
		Kw:  r.Kw,
		Ih:  r.Ih[:],
		T:   r.T,
		Pow: r.Pow,
		Sig: r.Sig[:],
	}
	out, err := bencode.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("companion: marshal record: %w", err)
	}
	if len(out) > MaxRecordBytes {
		return nil, fmt.Errorf("companion: encoded record %d bytes exceeds cap %d",
			len(out), MaxRecordBytes)
	}
	return out, nil
}

// DecodeRecord parses one bencoded record. The caller still has
// to verify the ed25519 signature and the hashcash PoW; this
// function only handles the transport form.
func DecodeRecord(raw []byte) (Record, error) {
	var wire recordWire
	if err := bencode.Unmarshal(raw, &wire); err != nil {
		return Record{}, fmt.Errorf("companion: unmarshal record: %w", err)
	}
	var r Record
	if len(wire.Pk) != 32 {
		return r, fmt.Errorf("companion: record pk %d bytes, want 32", len(wire.Pk))
	}
	if len(wire.Ih) != 20 {
		return r, fmt.Errorf("companion: record ih %d bytes, want 20", len(wire.Ih))
	}
	if len(wire.Sig) != 64 {
		return r, fmt.Errorf("companion: record sig %d bytes, want 64", len(wire.Sig))
	}
	if len(wire.Kw) > MaxKeywordBytes {
		return r, fmt.Errorf("companion: record keyword %d bytes exceeds cap %d",
			len(wire.Kw), MaxKeywordBytes)
	}
	copy(r.Pk[:], wire.Pk)
	r.Kw = wire.Kw
	copy(r.Ih[:], wire.Ih)
	r.T = wire.T
	r.Pow = wire.Pow
	copy(r.Sig[:], wire.Sig)
	return r, nil
}

// RecordKey is the sort key used for leaf ordering and interior
// separator selection. Spec: keyword || 0x00 || infohash. The NUL
// separator guarantees lex order matches the intuitive "all records
// for a keyword grouped contiguously, tie-broken by infohash".
func RecordKey(r Record) []byte {
	buf := make([]byte, 0, len(r.Kw)+1+20)
	buf = append(buf, r.Kw...)
	buf = append(buf, 0x00)
	buf = append(buf, r.Ih[:]...)
	return buf
}

// RecordSigMessage returns the bytes signed by the publisher.
// Kept separate from EncodeRecord because the signature itself is
// stored alongside the signed fields and must not be self-referential.
func RecordSigMessage(r Record) []byte {
	buf := make([]byte, 0, 32+len(r.Kw)+20+8+binary.MaxVarintLen64)
	buf = append(buf, r.Pk[:]...)
	buf = append(buf, r.Kw...)
	buf = append(buf, r.Ih[:]...)
	var ts [8]byte
	binary.LittleEndian.PutUint64(ts[:], uint64(r.T))
	buf = append(buf, ts[:]...)
	var nonce [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(nonce[:], r.Pow)
	buf = append(buf, nonce[:n]...)
	return buf
}

// VerifyRecordSig returns nil iff the record's signature is valid
// for the embedded pubkey. Does NOT verify hashcash difficulty —
// that lives in VerifyRecordPoW so the two policies can evolve
// independently.
func VerifyRecordSig(r Record) error {
	if !ed25519.Verify(ed25519.PublicKey(r.Pk[:]), RecordSigMessage(r), r.Sig[:]) {
		return errors.New("companion: record signature failed to verify")
	}
	return nil
}

// PageHeader captures the 16-byte fixed prefix every page carries.
type PageHeader struct {
	Version       uint8
	Kind          PageKind
	Level         uint8
	Flags         uint8
	PayloadLength uint16
}

func encodeHeader(h PageHeader) []byte {
	buf := make([]byte, PageHeaderSize)
	copy(buf[0:6], BTreeMagic[:])
	buf[6] = h.Version
	buf[7] = byte(h.Kind)
	buf[8] = h.Level
	buf[9] = h.Flags
	binary.LittleEndian.PutUint16(buf[10:12], h.PayloadLength)
	// buf[12:16] reserved, already zero from make()
	return buf
}

func decodeHeader(page []byte) (PageHeader, error) {
	var h PageHeader
	if len(page) < PageHeaderSize {
		return h, fmt.Errorf("companion: page %d bytes, need %d for header",
			len(page), PageHeaderSize)
	}
	if !bytes.Equal(page[0:6], BTreeMagic[:]) {
		return h, fmt.Errorf("companion: bad page magic %q, want %q",
			page[0:6], BTreeMagic[:])
	}
	h.Version = page[6]
	if h.Version != BTreeVersion {
		return h, fmt.Errorf("companion: page version %d unsupported (this build reads %d)",
			h.Version, BTreeVersion)
	}
	h.Kind = PageKind(page[7])
	h.Level = page[8]
	h.Flags = page[9]
	h.PayloadLength = binary.LittleEndian.Uint16(page[10:12])
	// bytes 12-15 reserved; we don't enforce zero here so that a
	// future writer may set flags there without breaking us.
	return h, nil
}

// InteriorChild is one entry in an interior or root page.
type InteriorChild struct {
	Separator []byte // smallest key in the subtree rooted at ChildIndex
	ChildIndex uint32 // piece index within the torrent
}

// EncodeInterior builds a full page (including header) for an
// interior or root node. pageSize caps the output; the function
// returns ErrPageOverflow when the child list does not fit.
func EncodeInterior(kind PageKind, level uint8, children []InteriorChild, pageSize int) ([]byte, error) {
	if kind != PageKindInterior && kind != PageKindRoot {
		return nil, fmt.Errorf("companion: EncodeInterior wrong kind %d", kind)
	}
	if len(children) == 0 {
		return nil, errors.New("companion: interior page needs ≥1 child")
	}
	if len(children) > 65535 {
		return nil, errors.New("companion: too many children for one page")
	}

	var payload bytes.Buffer
	var lenBuf [binary.MaxVarintLen64]byte
	var numBuf [2]byte
	binary.LittleEndian.PutUint16(numBuf[:], uint16(len(children)))
	payload.Write(numBuf[:])

	for i, c := range children {
		if i == 0 && len(c.Separator) != 0 {
			// The first child's separator is implicitly empty
			// (records less than the second child's separator
			// belong here). Writers MAY still emit it for clarity.
		}
		n := binary.PutUvarint(lenBuf[:], uint64(len(c.Separator)))
		payload.Write(lenBuf[:n])
		payload.Write(c.Separator)
		var idx [4]byte
		binary.LittleEndian.PutUint32(idx[:], c.ChildIndex)
		payload.Write(idx[:])
	}

	if payload.Len() > 65535 {
		return nil, errors.New("companion: interior payload exceeds uint16")
	}
	if PageHeaderSize+payload.Len() > pageSize {
		return nil, ErrPageOverflow
	}

	hdr := PageHeader{
		Version:       BTreeVersion,
		Kind:          kind,
		Level:         level,
		PayloadLength: uint16(payload.Len()),
	}
	out := make([]byte, pageSize)
	copy(out[:PageHeaderSize], encodeHeader(hdr))
	copy(out[PageHeaderSize:], payload.Bytes())
	// remainder stays zero-padded
	return out, nil
}

// DecodeInterior parses an interior or root page back into its
// child list. The returned slice aliases the input buffer for
// separator bytes; callers that hold it past the input's lifetime
// MUST copy.
func DecodeInterior(page []byte) (PageHeader, []InteriorChild, error) {
	hdr, err := decodeHeader(page)
	if err != nil {
		return hdr, nil, err
	}
	if hdr.Kind != PageKindInterior && hdr.Kind != PageKindRoot {
		return hdr, nil, fmt.Errorf("companion: expected interior/root, got kind 0x%02x", hdr.Kind)
	}
	if int(hdr.PayloadLength)+PageHeaderSize > len(page) {
		return hdr, nil, errors.New("companion: payload length exceeds page")
	}
	body := page[PageHeaderSize : PageHeaderSize+int(hdr.PayloadLength)]
	if len(body) < 2 {
		return hdr, nil, errors.New("companion: interior payload too short")
	}
	num := binary.LittleEndian.Uint16(body[:2])
	body = body[2:]
	children := make([]InteriorChild, 0, num)
	for i := 0; i < int(num); i++ {
		sepLen, n := binary.Uvarint(body)
		if n <= 0 {
			return hdr, nil, errors.New("companion: bad separator varint")
		}
		body = body[n:]
		if uint64(len(body)) < sepLen+4 {
			return hdr, nil, errors.New("companion: short separator or child index")
		}
		sep := body[:sepLen]
		body = body[sepLen:]
		idx := binary.LittleEndian.Uint32(body[:4])
		body = body[4:]
		children = append(children, InteriorChild{Separator: sep, ChildIndex: idx})
	}
	return hdr, children, nil
}

// EncodeLeaf builds one leaf page containing the supplied records
// in their given order. Returns ErrPageOverflow if the records
// don't fit in pageSize bytes. Caller is responsible for pre-
// sorting by RecordKey.
func EncodeLeaf(level uint8, records []Record, pageSize int) ([]byte, error) {
	if len(records) == 0 {
		return nil, errors.New("companion: leaf page needs ≥1 record")
	}
	if len(records) > 65535 {
		return nil, errors.New("companion: too many records for one leaf page")
	}

	var payload bytes.Buffer
	var numBuf [2]byte
	binary.LittleEndian.PutUint16(numBuf[:], uint16(len(records)))
	payload.Write(numBuf[:])
	var lenBuf [binary.MaxVarintLen64]byte

	for _, r := range records {
		enc, err := EncodeRecord(r)
		if err != nil {
			return nil, err
		}
		n := binary.PutUvarint(lenBuf[:], uint64(len(enc)))
		payload.Write(lenBuf[:n])
		payload.Write(enc)
	}

	if payload.Len() > 65535 {
		return nil, errors.New("companion: leaf payload exceeds uint16")
	}
	if PageHeaderSize+payload.Len() > pageSize {
		return nil, ErrPageOverflow
	}

	hdr := PageHeader{
		Version:       BTreeVersion,
		Kind:          PageKindLeaf,
		Level:         level,
		PayloadLength: uint16(payload.Len()),
	}
	out := make([]byte, pageSize)
	copy(out[:PageHeaderSize], encodeHeader(hdr))
	copy(out[PageHeaderSize:], payload.Bytes())
	return out, nil
}

// DecodeLeaf returns the records packed into a leaf page. As with
// DecodeInterior, decoded byte slices alias the input page.
func DecodeLeaf(page []byte) (PageHeader, []Record, error) {
	hdr, err := decodeHeader(page)
	if err != nil {
		return hdr, nil, err
	}
	if hdr.Kind != PageKindLeaf {
		return hdr, nil, fmt.Errorf("companion: expected leaf, got kind 0x%02x", hdr.Kind)
	}
	if int(hdr.PayloadLength)+PageHeaderSize > len(page) {
		return hdr, nil, errors.New("companion: payload length exceeds page")
	}
	body := page[PageHeaderSize : PageHeaderSize+int(hdr.PayloadLength)]
	if len(body) < 2 {
		return hdr, nil, errors.New("companion: leaf payload too short")
	}
	num := binary.LittleEndian.Uint16(body[:2])
	body = body[2:]
	records := make([]Record, 0, num)
	for i := 0; i < int(num); i++ {
		recLen, n := binary.Uvarint(body)
		if n <= 0 {
			return hdr, nil, errors.New("companion: bad record length varint")
		}
		body = body[n:]
		if uint64(len(body)) < recLen {
			return hdr, nil, errors.New("companion: short record bytes")
		}
		r, err := DecodeRecord(body[:recLen])
		if err != nil {
			return hdr, nil, err
		}
		records = append(records, r)
		body = body[recLen:]
	}
	return hdr, records, nil
}

// Trailer carries the global metadata for an index torrent, living
// in the last piece. Binds the full byte-stream to the publisher's
// signature so a reader can reject mutated pages without having to
// re-hash every leaf.
type Trailer struct {
	TrailerVersion  uint8
	PubKey          [32]byte
	Seq             uint64
	CreatedTs       uint64
	RootPieceIndex  uint32
	NumPages        uint32
	NumRecords      uint64
	MinPoWBits      uint8
	TreeFingerprint [32]byte
	PublisherSig    [64]byte
}

// encodeTrailerFields writes every field except PublisherSig into
// the canonical byte order used both for the final page payload
// and for the signing message. Keeping a single source of truth
// prevents the writer and the verifier from disagreeing on format.
func encodeTrailerFields(t Trailer) []byte {
	buf := make([]byte, 0, TrailerPayloadSize-64)
	buf = append(buf, t.TrailerVersion)
	buf = append(buf, t.PubKey[:]...)
	var u64 [8]byte
	binary.LittleEndian.PutUint64(u64[:], t.Seq)
	buf = append(buf, u64[:]...)
	binary.LittleEndian.PutUint64(u64[:], t.CreatedTs)
	buf = append(buf, u64[:]...)
	var u32 [4]byte
	binary.LittleEndian.PutUint32(u32[:], t.RootPieceIndex)
	buf = append(buf, u32[:]...)
	binary.LittleEndian.PutUint32(u32[:], t.NumPages)
	buf = append(buf, u32[:]...)
	binary.LittleEndian.PutUint64(u64[:], t.NumRecords)
	buf = append(buf, u64[:]...)
	buf = append(buf, t.MinPoWBits)
	buf = append(buf, t.TreeFingerprint[:]...)
	return buf
}

// TrailerSigMessage returns the bytes the publisher must sign so
// that PublisherSig authenticates the rest of the trailer.
func TrailerSigMessage(t Trailer) []byte {
	return encodeTrailerFields(t)
}

// EncodeTrailer writes the trailer page (header + payload) into a
// pageSize-sized buffer. pageSize MUST be ≥ PageHeaderSize +
// TrailerPayloadSize.
func EncodeTrailer(t Trailer, pageSize int) ([]byte, error) {
	if pageSize < PageHeaderSize+TrailerPayloadSize {
		return nil, fmt.Errorf("companion: page %d bytes too small for trailer (needs %d)",
			pageSize, PageHeaderSize+TrailerPayloadSize)
	}
	if t.TrailerVersion != 0x01 {
		return nil, fmt.Errorf("companion: unsupported trailer version %d", t.TrailerVersion)
	}

	payload := encodeTrailerFields(t)
	payload = append(payload, t.PublisherSig[:]...)
	if len(payload) != TrailerPayloadSize {
		return nil, fmt.Errorf("companion: trailer payload %d bytes, expected %d",
			len(payload), TrailerPayloadSize)
	}

	hdr := PageHeader{
		Version:       BTreeVersion,
		Kind:          PageKindTrailer,
		PayloadLength: uint16(len(payload)),
	}
	out := make([]byte, pageSize)
	copy(out[:PageHeaderSize], encodeHeader(hdr))
	copy(out[PageHeaderSize:], payload)
	return out, nil
}

// DecodeTrailer parses a trailer page into its structured form.
// It does NOT verify the publisher signature — the caller must
// run VerifyTrailerSig after decode succeeds.
func DecodeTrailer(page []byte) (Trailer, error) {
	var t Trailer
	hdr, err := decodeHeader(page)
	if err != nil {
		return t, err
	}
	if hdr.Kind != PageKindTrailer {
		return t, fmt.Errorf("companion: expected trailer, got kind 0x%02x", hdr.Kind)
	}
	if int(hdr.PayloadLength) != TrailerPayloadSize {
		return t, fmt.Errorf("companion: trailer payload length %d, expected %d",
			hdr.PayloadLength, TrailerPayloadSize)
	}
	body := page[PageHeaderSize : PageHeaderSize+TrailerPayloadSize]
	pos := 0
	t.TrailerVersion = body[pos]
	pos++
	if t.TrailerVersion != 0x01 {
		return t, fmt.Errorf("companion: unsupported trailer version %d", t.TrailerVersion)
	}
	copy(t.PubKey[:], body[pos:pos+32])
	pos += 32
	t.Seq = binary.LittleEndian.Uint64(body[pos : pos+8])
	pos += 8
	t.CreatedTs = binary.LittleEndian.Uint64(body[pos : pos+8])
	pos += 8
	t.RootPieceIndex = binary.LittleEndian.Uint32(body[pos : pos+4])
	pos += 4
	t.NumPages = binary.LittleEndian.Uint32(body[pos : pos+4])
	pos += 4
	t.NumRecords = binary.LittleEndian.Uint64(body[pos : pos+8])
	pos += 8
	t.MinPoWBits = body[pos]
	pos++
	copy(t.TreeFingerprint[:], body[pos:pos+32])
	pos += 32
	copy(t.PublisherSig[:], body[pos:pos+64])
	return t, nil
}

// VerifyTrailerSig returns nil iff PublisherSig is a valid ed25519
// signature over the rest of the trailer, using the embedded
// PubKey. Callers MUST run this before trusting any data derived
// from the tree.
func VerifyTrailerSig(t Trailer) error {
	if !ed25519.Verify(ed25519.PublicKey(t.PubKey[:]), TrailerSigMessage(t), t.PublisherSig[:]) {
		return errors.New("companion: trailer signature failed to verify")
	}
	return nil
}

// ErrPageOverflow is returned by EncodeInterior / EncodeLeaf when
// the supplied contents don't fit in the requested pageSize. The
// build path in build.go uses this as a signal to split the
// current page and start a new one.
var ErrPageOverflow = errors.New("companion: page overflow")
