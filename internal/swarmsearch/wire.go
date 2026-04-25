package swarmsearch

import (
	"fmt"

	"github.com/anacrolix/torrent/bencode"
)

// Message-type discriminators for the sn_search extension, per
// docs/05-integration-design.md §5.2. Every sn_search wire message is a
// bencoded dict with a top-level "msg_type" integer that selects which
// inner schema to use.
const (
	MsgTypeQuery        = 0
	MsgTypeResult       = 1
	MsgTypeReject       = 2
	MsgTypePeerAnnounce = 3
)

// Reject codes, per §5.2.3 of the design doc.
const (
	RejectRateLimited      = 0
	RejectTooExpensive     = 1
	RejectUnsupportedScope = 2
	RejectQueryTooBroad    = 3
	RejectShuttingDown     = 4
)

// Default scope value. "n" = torrent-name match, "f" = file-list match,
// "c" = content-level match. The string is the union of single-letter
// flags.
const DefaultScope = "nfc"

// Query is the decoded form of an sn_search query message (msg_type 0).
// Field tags match the short-name bencode schema from §5.2.1.
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

// Result is the decoded form of an sn_search result message (msg_type 1).
type Result struct {
	MsgType int    `bencode:"msg_type"`
	TxID    uint32 `bencode:"txid"`
	Total   int    `bencode:"total,omitempty"`
	Partial int    `bencode:"partial,omitempty"`
	Hits    []Hit  `bencode:"hits"`
}

// Hit is one entry in the Result.Hits list. Bencode tags are the short
// names from the design doc so we stay wire-compatible with any other
// sn_search implementation that reads the spec directly.
type Hit struct {
	// IH is the 20-byte SHA-1 infohash of the torrent this hit
	// belongs to.
	IH []byte `bencode:"ih"`
	// IH2 is the 32-byte SHA-256 hybrid infohash (BEP-52). Optional;
	// omitted for v1-only torrents.
	IH2 []byte `bencode:"ih2,omitempty"`
	// N is a short torrent name, truncated to roughly 60 bytes to keep
	// packet sizes bounded.
	N string `bencode:"n"`
	// S is the seeders count as last observed by the responder.
	S int `bencode:"s,omitempty"`
	// L is the leechers count.
	L int `bencode:"l,omitempty"`
	// Sz is the total torrent size in bytes.
	Sz int64 `bencode:"sz,omitempty"`
	// T is a unix timestamp for when the responder added this torrent
	// to their index. Used for freshness ranking.
	T int64 `bencode:"t,omitempty"`
	// Rank is the responder's own score for this hit, scaled 0-1000.
	Rank int `bencode:"rank,omitempty"`
	// Matches is the per-file match list. Populated only when the
	// query asked for scope containing 'f' or 'c' AND the responder
	// has corresponding data.
	Matches []FileMatch `bencode:"matches,omitempty"`
}

// FileMatch is one entry in the Hit.Matches list. Each corresponds to a
// specific file inside the hit torrent that matched the query.
type FileMatch struct {
	FI int    `bencode:"fi"`           // file index in torrent
	FP string `bencode:"fp,omitempty"` // file path
	// PR is the BEP-52 per-file pieces root. Optional.
	PR []byte `bencode:"pr,omitempty"`
	// Sn is a short context snippet around the match (M3b stores
	// plain filenames/summary; M3c+ may fetch actual text snippets).
	Sn string `bencode:"sn,omitempty"`
	// Off is the byte offset within the source file where the match
	// starts. 0 when unknown.
	Off int64 `bencode:"off,omitempty"`
}

// Reject is the decoded form of a reject message (msg_type 2).
type Reject struct {
	MsgType int    `bencode:"msg_type"`
	TxID    uint32 `bencode:"txid"`
	Code    int    `bencode:"code"`
	Reason  string `bencode:"reason,omitempty"`
}

// EncodeQuery serialises a Query into the bencoded byte form that goes
// on the wire inside an LTEP extended message.
func EncodeQuery(q Query) ([]byte, error) {
	q.MsgType = MsgTypeQuery
	return bencode.Marshal(q)
}

// EncodeResult serialises a Result message.
func EncodeResult(r Result) ([]byte, error) {
	r.MsgType = MsgTypeResult
	// Bencode requires a non-nil slice to encode a list, even if empty.
	if r.Hits == nil {
		r.Hits = []Hit{}
	}
	return bencode.Marshal(r)
}

// EncodeReject serialises a Reject message.
func EncodeReject(rj Reject) ([]byte, error) {
	rj.MsgType = MsgTypeReject
	return bencode.Marshal(rj)
}

// messageHeader is a minimal struct used to peek at the msg_type field
// of an inbound payload before dispatching to a concrete Decode
// function. It is only used internally.
type messageHeader struct {
	MsgType int    `bencode:"msg_type"`
	TxID    uint32 `bencode:"txid"`
}

// peekHeader returns the msg_type (and txid) of an inbound payload
// without fully decoding it. Returns a descriptive error if the bytes
// are not a bencoded dict.
func peekHeader(payload []byte) (messageHeader, error) {
	var h messageHeader
	if err := bencode.Unmarshal(payload, &h); err != nil {
		return h, fmt.Errorf("swarmsearch: peek header: %w", err)
	}
	return h, nil
}

// DecodeQuery parses a Query message from its bencoded form. It does
// not verify msg_type — callers should have already checked via
// peekHeader.
func DecodeQuery(payload []byte) (Query, error) {
	var q Query
	if err := bencode.Unmarshal(payload, &q); err != nil {
		return q, fmt.Errorf("swarmsearch: decode query: %w", err)
	}
	if q.MsgType != MsgTypeQuery {
		return q, fmt.Errorf("swarmsearch: not a query, msg_type=%d", q.MsgType)
	}
	return q, nil
}

// DecodeResult parses a Result message.
func DecodeResult(payload []byte) (Result, error) {
	var r Result
	if err := bencode.Unmarshal(payload, &r); err != nil {
		return r, fmt.Errorf("swarmsearch: decode result: %w", err)
	}
	if r.MsgType != MsgTypeResult {
		return r, fmt.Errorf("swarmsearch: not a result, msg_type=%d", r.MsgType)
	}
	return r, nil
}

// DecodeReject parses a Reject message.
func DecodeReject(payload []byte) (Reject, error) {
	var rj Reject
	if err := bencode.Unmarshal(payload, &rj); err != nil {
		return rj, fmt.Errorf("swarmsearch: decode reject: %w", err)
	}
	if rj.MsgType != MsgTypeReject {
		return rj, fmt.Errorf("swarmsearch: not a reject, msg_type=%d", rj.MsgType)
	}
	return rj, nil
}

// PeerAnnounce is msg_type 3: a one-shot announcement a peer
// sends after the LTEP handshake to declare its ServiceBits
// and protocol version. Fires once per connection direction
// (initiator → target AND target → initiator). The recipient
// stores the announced services on the peer's PeerState so the
// query fan-out can filter by capability without a round trip.
//
// Unknown bits in the Services field MUST be ignored per the
// M15b invariant. Old peers that don't send PeerAnnounce are
// treated as "services unknown" (zero mask), which is fine —
// they're still queried normally; they just can't be filtered.
//
// Pubkey is the sender's own 32-byte ed25519 Layer-D publisher
// identity. Absent when the sender isn't running the keyword
// publisher. This is the gossip primitive for §4.3's "gossip-
// discovered indexer pubkeys": a receiver that also runs the
// publisher adds `pk` to its Lookup's known-indexer set so
// subsequent DHT keyword GETs fan out to it too. Closes wire-
// compat matrix row 8.4-C.
type PeerAnnounce struct {
	MsgType  int    `bencode:"msg_type"`
	Version  int    `bencode:"v"`                  // ProtocolVersion
	Services uint64 `bencode:"services,omitempty"` // ServiceBits as uint64
	Pubkey   []byte `bencode:"pk,omitempty"`       // 32-byte ed25519 publisher key
	// Endorsed is the SPEC §3.3 channel-C gossip primitive —
	// 32-byte ed25519 publisher pubkeys the sender vouches for.
	// Subscribers route these to their Bootstrap's
	// IngestEndorsement so admission thresholds can fire. Capped
	// at MaxEndorsedPerAnnounce entries per frame to keep per-
	// handshake overhead bounded.
	Endorsed [][]byte `bencode:"endorsed,omitempty"`
}

// MaxEndorsedPerAnnounce caps the per-frame endorsement list
// size. SPEC §3.3 suggests 10; we enforce it on both encode and
// decode so malformed peers can't over-claim.
const MaxEndorsedPerAnnounce = 10

// EncodePeerAnnounce serialises a PeerAnnounce message.
func EncodePeerAnnounce(pa PeerAnnounce) ([]byte, error) {
	pa.MsgType = MsgTypePeerAnnounce
	if len(pa.Endorsed) > MaxEndorsedPerAnnounce {
		return nil, fmt.Errorf("swarmsearch: %d endorsements exceeds cap %d",
			len(pa.Endorsed), MaxEndorsedPerAnnounce)
	}
	for i, e := range pa.Endorsed {
		if len(e) != 32 {
			return nil, fmt.Errorf("swarmsearch: endorsed[%d] has %d bytes, want 32", i, len(e))
		}
	}
	return bencode.Marshal(pa)
}

// DecodePeerAnnounce parses a PeerAnnounce message. Silently
// drops endorsements with the wrong byte length rather than
// failing the whole frame — a malformed entry is a local
// validation fault, not a protocol-level violation.
func DecodePeerAnnounce(payload []byte) (PeerAnnounce, error) {
	var pa PeerAnnounce
	if err := bencode.Unmarshal(payload, &pa); err != nil {
		return pa, fmt.Errorf("swarmsearch: decode peer_announce: %w", err)
	}
	if pa.MsgType != MsgTypePeerAnnounce {
		return pa, fmt.Errorf("swarmsearch: not a peer_announce, msg_type=%d", pa.MsgType)
	}
	// Cap endorsement count defensively — a noisy peer shouldn't
	// blow out memory. Extra entries beyond the cap are truncated.
	if len(pa.Endorsed) > MaxEndorsedPerAnnounce {
		pa.Endorsed = pa.Endorsed[:MaxEndorsedPerAnnounce]
	}
	// Filter malformed entries. The remainder is still a valid
	// frame from the caller's perspective.
	clean := pa.Endorsed[:0]
	for _, e := range pa.Endorsed {
		if len(e) == 32 {
			clean = append(clean, e)
		}
	}
	pa.Endorsed = clean
	return pa, nil
}
