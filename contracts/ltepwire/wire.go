package ltepwire

// This file is the frozen sn_search message envelope: the query/result/reject/
// peer_announce bodies (msg_types 0–3) carried as the payload of a standard
// BEP-10 extended message. The RIBLT sync bodies (msg_types 4–8) are reserved
// here and defined in a later slice. The bencode key names, their sort order
// (anacrolix bencode.Marshal sorts lexicographically), the optionality, and the
// size caps are all part of the wire contract — a second implementation must
// produce byte-identical frames.

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/anacrolix/torrent/bencode"
)

// ProtocolVersion is the sn_search protocol version, carried in the `v` field
// of every peer_announce.
const ProtocolVersion = 1

// ExtensionName is the LTEP `m`-dict key under which sn_search is advertised.
// The `sn_` prefix stays clear of libtorrent's `lt_` namespace.
const ExtensionName = "sn_search"

// DefaultScope is the query scope a node uses when none is specified: the union
// of name (`n`), file-list (`f`), and content (`c`) matching.
const DefaultScope = "nfc"

// MaxEndorsedPerAnnounce caps the endorsement list in a peer_announce. Enforced
// asymmetrically: encode errors on overflow, decode truncates + filters.
const MaxEndorsedPerAnnounce = 10

// MaxHitNameBytes bounds a hit's name on the wire (rune-boundary-safe). The
// legacy documented "~60 bytes" but never truncated — an unbounded name is a
// wire-bloat vector (§6 defect c).
const MaxHitNameBytes = 60

// Message-type discriminators. FROZEN and append-only — never renumber. 0–3 are
// built here; 4–8 are reserved for the RIBLT sync slice.
const (
	MsgTypeQuery        = 0
	MsgTypeResult       = 1
	MsgTypeReject       = 2
	MsgTypePeerAnnounce = 3
	MsgTypeSyncBegin    = 4 // reserved (Slice 8)
	MsgTypeSyncSymbols  = 5 // reserved (Slice 8)
	MsgTypeSyncNeed     = 6 // reserved (Slice 8)
	MsgTypeSyncRecords  = 7 // reserved (Slice 8)
	MsgTypeSyncEnd      = 8 // reserved (Slice 8)
)

// Reject codes carried in the `code` field of a reject frame.
const (
	RejectRateLimited      = 0
	RejectTooExpensive     = 1
	RejectUnsupportedScope = 2
	RejectQueryTooBroad    = 3
	RejectShuttingDown     = 4
)

// Query is msg_type 0. Q and TxID are required; the rest are omitempty. Lang/
// MinSize/MaxSize/NotIH are decoded and round-tripped but the handler ignores
// them (spec-tolerated MAY filters).
type Query struct {
	MsgType int      `bencode:"msg_type"`
	TxID    uint32   `bencode:"txid"`
	Q       string   `bencode:"q"`
	Scope   string   `bencode:"scope,omitempty"`
	Limit   int      `bencode:"limit,omitempty"`
	Lang    string   `bencode:"lang,omitempty"`
	MinSize int64    `bencode:"min_size,omitempty"`
	MaxSize int64    `bencode:"max_size,omitempty"`
	NotIH   [][]byte `bencode:"not_ih,omitempty"`
}

// FileMatch is one per-file match inside a Hit.
type FileMatch struct {
	FI  int    `bencode:"fi"`
	FP  string `bencode:"fp,omitempty"`
	Off int64  `bencode:"off,omitempty"`
	Pr  []byte `bencode:"pr,omitempty"` // 32-byte BEP-52 pieces root
	Sn  string `bencode:"sn,omitempty"`
}

// Hit is one torrent in a result. IH is exactly 20 bytes (SHA-1); N is the
// name (truncated on encode); T is a unix seconds added-at stamp, emitted only
// when non-zero (never the year-1 zero stamp, §6 defect b).
type Hit struct {
	IH      []byte      `bencode:"ih"`
	IH2     []byte      `bencode:"ih2,omitempty"` // 32-byte BEP-52 infohash
	N       string      `bencode:"n"`
	S       int         `bencode:"s,omitempty"` // seeders
	L       int         `bencode:"l,omitempty"` // leechers
	Sz      int64       `bencode:"sz,omitempty"`
	T       int64       `bencode:"t,omitempty"` // added-at unix seconds
	Rank    int         `bencode:"rank,omitempty"`
	Matches []FileMatch `bencode:"matches,omitempty"`
}

// Result is msg_type 1. Hits is NOT omitempty — a nil slice is forced to an
// empty list so an empty result encodes as `hits` `le`, distinct from absent.
type Result struct {
	MsgType int    `bencode:"msg_type"`
	TxID    uint32 `bencode:"txid"`
	Total   int    `bencode:"total,omitempty"`
	Partial int    `bencode:"partial,omitempty"` // reserved; never set by the handler
	Hits    []Hit  `bencode:"hits"`
}

// Reject is msg_type 2.
type Reject struct {
	MsgType int    `bencode:"msg_type"`
	TxID    uint32 `bencode:"txid"`
	Code    int    `bencode:"code"`
	Reason  string `bencode:"reason,omitempty"`
}

// PeerAnnounce is msg_type 3. It carries NO txid. Services is the 64-bit mask
// from Announced() (a bencode integer, omitempty so services=0 is absent). Pk
// is a 32-byte ed25519 key, present iff the node is publishing. Endorsed is
// ≤MaxEndorsedPerAnnounce 32-byte keys.
type PeerAnnounce struct {
	MsgType  int      `bencode:"msg_type"`
	Version  int      `bencode:"v"`
	Services uint64   `bencode:"services,omitempty"`
	Pk       []byte   `bencode:"pk,omitempty"`
	Endorsed [][]byte `bencode:"endorsed,omitempty"`
}

// peekHeader is the minimal decode used to discriminate an inbound frame: it
// reads only msg_type (and, when present, txid) so the dispatcher can route
// before a full typed decode. A non-bencode / non-dict payload errors.
type peekHeader struct {
	MsgType int    `bencode:"msg_type"`
	TxID    uint32 `bencode:"txid"`
}

// PeekMsgType returns the msg_type discriminator of an inbound frame, or an
// error if the payload is not a bencoded dict (charge ScoreBadBencode).
func PeekMsgType(payload []byte) (int, error) {
	var h peekHeader
	if err := decodeBounded(payload, &h); err != nil {
		return 0, fmt.Errorf("ltepwire: bad header: %w", err)
	}
	return h.MsgType, nil
}

// PeekTxID reads the txid of an inbound frame regardless of its msg_type (0 for
// a frame without one, e.g. peer_announce, or an unparseable payload). Used to
// echo the txid in a reject without a full typed decode.
func PeekTxID(payload []byte) uint32 {
	var h peekHeader
	if err := decodeBounded(payload, &h); err != nil {
		return 0
	}
	return h.TxID
}

var errEndorsedCap = errors.New("ltepwire: endorsed exceeds cap")

// EncodeQuery stamps msg_type and marshals.
func EncodeQuery(q Query) ([]byte, error) {
	q.MsgType = MsgTypeQuery
	return bencode.Marshal(q)
}

// EncodeResult stamps msg_type, forces a non-nil Hits (empty list, never
// null), and marshals. Each hit is normalized: name truncated to
// MaxHitNameBytes (rune-safe), rank clamped 0..1000, and a negative/zero T
// dropped so the codec never emits the year-1 stamp.
func EncodeResult(r Result) ([]byte, error) {
	r.MsgType = MsgTypeResult
	if r.Hits == nil {
		r.Hits = []Hit{}
	}
	norm := make([]Hit, len(r.Hits))
	for i, h := range r.Hits {
		h.N = truncateUTF8(h.N, MaxHitNameBytes)
		h.Rank = clampRank(h.Rank)
		if h.T < 0 {
			h.T = 0 // never emit a negative added-at stamp
		}
		for j := range h.Matches {
			h.Matches[j].FP = truncateUTF8(h.Matches[j].FP, MaxHitNameBytes)
		}
		norm[i] = h
	}
	r.Hits = norm
	return bencode.Marshal(r)
}

// EncodeReject stamps msg_type and marshals.
func EncodeReject(r Reject) ([]byte, error) {
	r.MsgType = MsgTypeReject
	return bencode.Marshal(r)
}

// EncodePeerAnnounce stamps msg_type + version and marshals. It ERRORS on an
// oversized or wrong-length endorsement list (the asymmetric-encode half).
func EncodePeerAnnounce(pa PeerAnnounce) ([]byte, error) {
	pa.MsgType = MsgTypePeerAnnounce
	pa.Version = ProtocolVersion
	if len(pa.Endorsed) > MaxEndorsedPerAnnounce {
		return nil, errEndorsedCap
	}
	for _, e := range pa.Endorsed {
		if len(e) != 32 {
			return nil, fmt.Errorf("ltepwire: endorsed entry must be 32 bytes, got %d", len(e))
		}
	}
	return bencode.Marshal(pa)
}

// DecodeQuery unmarshals and re-asserts msg_type == query.
func DecodeQuery(payload []byte) (Query, error) {
	var q Query
	if err := decodeBounded(payload, &q); err != nil {
		return Query{}, err
	}
	if q.MsgType != MsgTypeQuery {
		return Query{}, fmt.Errorf("ltepwire: not a query, msg_type=%d", q.MsgType)
	}
	return q, nil
}

// DecodeResult unmarshals and re-asserts msg_type == result.
func DecodeResult(payload []byte) (Result, error) {
	var r Result
	if err := decodeBounded(payload, &r); err != nil {
		return Result{}, err
	}
	if r.MsgType != MsgTypeResult {
		return Result{}, fmt.Errorf("ltepwire: not a result, msg_type=%d", r.MsgType)
	}
	return r, nil
}

// DecodeReject unmarshals and re-asserts msg_type == reject.
func DecodeReject(payload []byte) (Reject, error) {
	var r Reject
	if err := decodeBounded(payload, &r); err != nil {
		return Reject{}, err
	}
	if r.MsgType != MsgTypeReject {
		return Reject{}, fmt.Errorf("ltepwire: not a reject, msg_type=%d", r.MsgType)
	}
	return r, nil
}

// DecodePeerAnnounce unmarshals, re-asserts msg_type, and cleans the
// endorsement list: it truncates to MaxEndorsedPerAnnounce and drops any entry
// that is not exactly 32 bytes (the asymmetric-decode half — a wrong-length
// entry never fails the frame).
func DecodePeerAnnounce(payload []byte) (PeerAnnounce, error) {
	var pa PeerAnnounce
	if err := decodeBounded(payload, &pa); err != nil {
		return PeerAnnounce{}, err
	}
	if pa.MsgType != MsgTypePeerAnnounce {
		return PeerAnnounce{}, fmt.Errorf("ltepwire: not a peer_announce, msg_type=%d", pa.MsgType)
	}
	if len(pa.Endorsed) > 0 {
		clean := pa.Endorsed[:0]
		for _, e := range pa.Endorsed {
			if len(clean) >= MaxEndorsedPerAnnounce {
				break
			}
			if len(e) == 32 {
				clean = append(clean, e)
			}
		}
		pa.Endorsed = clean
	}
	return pa, nil
}

func clampRank(r int) int {
	if r < 0 {
		return 0
	}
	if r > 1000 {
		return 1000
	}
	return r
}

// truncateUTF8 returns s truncated to at most max bytes without splitting a
// multi-byte rune. It walks runes from the FRONT (not back-stripping from the
// cut point): DecodeRuneInString yields (RuneError, 1) for an invalid byte, so
// a non-UTF-8 name degrades to a byte cap at max rather than collapsing to the
// empty string (which back-stripping would do when the invalid byte is near
// the head).
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	i := 0
	for i < len(s) {
		_, size := utf8.DecodeRuneInString(s[i:])
		if i+size > max {
			break
		}
		i += size
	}
	return s[:i]
}
