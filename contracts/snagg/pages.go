package snagg

import (
	"encoding/binary"
	"fmt"
)

// EncodeLeaf writes a leaf page: header + [num_records u16][ uvarint(len)+enc ]*.
// Returns ErrPageOverflow when it does not fit pageSize (the split signal).
func EncodeLeaf(level int, records []Record, pageSize int) ([]byte, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("snagg: leaf page needs ≥1 record")
	}
	if len(records) > 0xFFFF {
		return nil, fmt.Errorf("snagg: too many records for one leaf page")
	}
	payload := make([]byte, 2)
	binary.LittleEndian.PutUint16(payload, uint16(len(records)))
	var scratch [binary.MaxVarintLen64]byte
	for _, r := range records {
		enc, err := EncodeRecord(r)
		if err != nil {
			return nil, err
		}
		n := binary.PutUvarint(scratch[:], uint64(len(enc)))
		payload = append(payload, scratch[:n]...)
		payload = append(payload, enc...)
	}
	if len(payload) > 0xFFFF {
		return nil, fmt.Errorf("snagg: leaf payload exceeds uint16")
	}
	if PageHeaderSize+len(payload) > pageSize {
		return nil, ErrPageOverflow
	}
	page := make([]byte, pageSize)
	encodeHeader(page, pageHeader{kind: PageKindLeaf, level: uint8(level), payload: uint16(len(payload))})
	copy(page[PageHeaderSize:], payload)
	return page, nil
}

// DecodeLeaf parses a leaf page. Returned records copy out of the page buffer.
func DecodeLeaf(page []byte) ([]Record, error) {
	h, err := decodeHeader(page)
	if err != nil {
		return nil, err
	}
	if h.kind != PageKindLeaf {
		return nil, fmt.Errorf("snagg: expected leaf, got kind 0x%02x", h.kind)
	}
	if PageHeaderSize+int(h.payload) > len(page) {
		return nil, fmt.Errorf("snagg: payload length exceeds page")
	}
	p := page[PageHeaderSize : PageHeaderSize+int(h.payload)]
	if len(p) < 2 {
		return nil, fmt.Errorf("snagg: leaf payload too short")
	}
	n := binary.LittleEndian.Uint16(p)
	p = p[2:]
	// Cap the pre-allocation against the remaining bytes: each record needs at
	// least a 1-byte length varint, so a payload of len(p) bytes can hold at most
	// len(p) records. A hostile count (up to 65535) in a structurally-
	// unauthenticated page would otherwise pre-allocate ~10 MB before the loop
	// discovers there are no record bytes (CWE-789). The loop still errors on the
	// first missing byte; this only bounds the speculative make.
	out := make([]Record, 0, capHint(int(n), len(p)))
	for i := 0; i < int(n); i++ {
		rl, adv := binary.Uvarint(p)
		if adv <= 0 {
			return nil, fmt.Errorf("snagg: bad record length varint")
		}
		p = p[adv:]
		if uint64(len(p)) < rl {
			return nil, fmt.Errorf("snagg: short record bytes")
		}
		rec, err := DecodeRecord(p[:rl])
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
		p = p[rl:]
	}
	return out, nil
}

// capHint bounds a slice pre-allocation hint (an untrusted element count) by the
// number of bytes that could actually encode that many elements, so a hostile
// count can never drive a large speculative allocation before the decode loop
// validates the bytes.
func capHint(n, remaining int) int {
	if n > remaining {
		return remaining
	}
	return n
}

// InteriorChild is one child pointer: Separator = the min RecordKey of the
// child's subtree (empty for the first child ⇒ −∞); ChildIndex = piece index.
type InteriorChild struct {
	Separator  []byte
	ChildIndex uint32
}

// EncodeInterior writes an interior or root page. The first child's separator
// is FORCED empty regardless of input (its lower bound is always −∞).
func EncodeInterior(kind uint8, level int, children []InteriorChild, pageSize int) ([]byte, error) {
	if kind != PageKindInterior && kind != PageKindRoot {
		return nil, fmt.Errorf("snagg: EncodeInterior wrong kind %d", kind)
	}
	if len(children) == 0 {
		return nil, fmt.Errorf("snagg: interior page needs ≥1 child")
	}
	if len(children) > 0xFFFF {
		return nil, fmt.Errorf("snagg: too many children for one page")
	}
	payload := make([]byte, 2)
	binary.LittleEndian.PutUint16(payload, uint16(len(children)))
	var scratch [binary.MaxVarintLen64]byte
	for i, c := range children {
		sep := c.Separator
		if i == 0 {
			sep = nil
		}
		n := binary.PutUvarint(scratch[:], uint64(len(sep)))
		payload = append(payload, scratch[:n]...)
		payload = append(payload, sep...)
		var idx [4]byte
		binary.LittleEndian.PutUint32(idx[:], c.ChildIndex)
		payload = append(payload, idx[:]...)
	}
	if len(payload) > 0xFFFF {
		return nil, fmt.Errorf("snagg: interior payload exceeds uint16")
	}
	if PageHeaderSize+len(payload) > pageSize {
		return nil, ErrPageOverflow
	}
	page := make([]byte, pageSize)
	encodeHeader(page, pageHeader{kind: kind, level: uint8(level), payload: uint16(len(payload))})
	copy(page[PageHeaderSize:], payload)
	return page, nil
}

// DecodeInterior parses an interior/root page. Separator slices copy out.
func DecodeInterior(page []byte) ([]InteriorChild, error) {
	h, err := decodeHeader(page)
	if err != nil {
		return nil, err
	}
	if h.kind != PageKindInterior && h.kind != PageKindRoot {
		return nil, fmt.Errorf("snagg: expected interior/root, got kind 0x%02x", h.kind)
	}
	if PageHeaderSize+int(h.payload) > len(page) {
		return nil, fmt.Errorf("snagg: payload length exceeds page")
	}
	p := page[PageHeaderSize : PageHeaderSize+int(h.payload)]
	if len(p) < 2 {
		return nil, fmt.Errorf("snagg: interior payload too short")
	}
	n := binary.LittleEndian.Uint16(p)
	p = p[2:]
	// Cap the pre-allocation against the remaining bytes (see DecodeLeaf): each
	// child needs at least a 1-byte separator-length varint, so len(p) bounds the
	// child count. Prevents a hostile uint16 count from pre-allocating ~2 MB.
	out := make([]InteriorChild, 0, capHint(int(n), len(p)))
	for i := 0; i < int(n); i++ {
		sl, adv := binary.Uvarint(p)
		if adv <= 0 {
			return nil, fmt.Errorf("snagg: bad separator varint")
		}
		p = p[adv:]
		// Overflow-safe bounds check: `sl+4` would wrap for an attacker-set sl
		// near 2^64 (a valid 10-byte uvarint), letting p[:sl] panic. Compare the
		// separator length against the buffer WITHOUT adding to it, then require
		// 4 more bytes for the child index.
		if sl > uint64(len(p)) || uint64(len(p))-sl < 4 {
			return nil, fmt.Errorf("snagg: short separator or child index")
		}
		sep := append([]byte(nil), p[:sl]...)
		p = p[sl:]
		idx := binary.LittleEndian.Uint32(p[:4])
		p = p[4:]
		out = append(out, InteriorChild{Separator: sep, ChildIndex: idx})
	}
	return out, nil
}
