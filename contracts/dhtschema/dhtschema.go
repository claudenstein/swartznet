// Package dhtschema is the frozen wire contract for the BEP-44 keyword index
// (Layer D). A KeywordValue is the bencoded payload stored at the DHT target
// SHA1(publisher_pubkey || salt), where salt is the keyword bytes verbatim.
//
// This is a cross-implementation contract: a second implementation MUST
// produce byte-identical output for the same input, because the encoded value
// is what a vanilla BEP-44 client verifies and any SwartzNet reader decodes.
// The short bencode key names ("ts", "hits", "ih", "n", "s", "f", "sz"), the
// 1000-byte cap, the ≤64-byte salt cap, the reserved-but-unpopulated "more"
// and "next_pk" fields, and the reject-before-unmarshal ordering are all part
// of the frozen contract. Golden vectors in the test pin the bytes.
//
// The package uses anacrolix/torrent/bencode (NOT contracts/bencode): the
// production put path re-decodes EncodeValue output into an interface{} and
// hands it to bep44.Put.Sign, which re-marshals with the same library, so the
// signed bytes must be byte-identical to what this package produced. Only
// anacrolix's marshaller guarantees that round-trip identity.
package dhtschema

import (
	"errors"
	"fmt"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// MaxSaltBytes is BEP-44's hard cap on the salt field. A keyword whose UTF-8
// form exceeds this is dropped rather than truncated: truncation could make
// two distinct keywords collide onto one DHT target.
const MaxSaltBytes = 64

// MaxValueBytes is BEP-44's hard cap on the bencoded `v` field of a mutable
// item. EncodeValue rejects any KeywordValue whose encoded form exceeds this;
// DecodeValue rejects any remote payload over it BEFORE unmarshalling. To stay
// under the cap the publisher evicts the oldest hit from an entry; multi-shard
// spill (KeywordValue.More) is reserved scaffolding, not wired in v1.
const MaxValueBytes = 1000

// KeywordValue is the bencoded payload stored at SHA1(publisher_pubkey||salt).
// It is the unit of publication for one (publisher, keyword) pair. Field names
// are deliberately short so ~25-40 hits fit under the 1000-byte cap; long
// names belong in the local index, not the DHT.
type KeywordValue struct {
	// Ts is the unix timestamp at which this snapshot was generated. Mostly
	// informational; the BEP-44 sequence number is what actually orders
	// updates. EncodeValue stamps time.Now().Unix() when it is left zero.
	Ts int64 `bencode:"ts"`

	// Hits is the list of torrent hits this publisher claims for the keyword.
	Hits []KeywordHit `bencode:"hits"`

	// More is reserved for a future multi-shard scheme: when set to 1 it would
	// signal additional shards at salts "<keyword>#1", "<keyword>#2", … (see
	// SaltForShard). v1 NEVER sets it and Lookup never follows it; oversize
	// entries are bounded by evicting the oldest hit. The field ships on the
	// wire (omitted while zero) so the spill scheme can be added without a
	// format bump.
	More int `bencode:"more,omitempty"`

	// NextPubKey is an optional 32-byte ed25519 public key pointing to the
	// publisher's next key in a rotation chain, signed by the current key via
	// the BEP-44 mutable item. v1 ships the field on the wire but never
	// rotates — NextPubKey is always empty in v1 puts. Present in the v1
	// schema so future clients need no format bump to start using it.
	NextPubKey []byte `bencode:"next_pk,omitempty"`
}

// KeywordHit is one entry in the Hits list. The field names mirror the
// sn_search wire format so consumers can reuse the same hit type internally.
type KeywordHit struct {
	IH []byte `bencode:"ih"`           // 20-byte SHA-1 infohash
	N  string `bencode:"n,omitempty"`  // short torrent name
	S  int    `bencode:"s,omitempty"`  // seeders count (last seen)
	F  int    `bencode:"f,omitempty"`  // file count
	Sz int64  `bencode:"sz,omitempty"` // size in bytes
}

// EncodeValue serialises a KeywordValue. It fills in Ts with the current unix
// time when the caller left it zero so every DHT entry carries a fresh
// timestamp, normalizes a nil Hits to the empty slice, and rejects an encoded
// form larger than MaxValueBytes (inclusive: exactly 1000 bytes is accepted).
func EncodeValue(v KeywordValue) ([]byte, error) {
	if v.Ts == 0 {
		v.Ts = time.Now().Unix()
	}
	if v.Hits == nil {
		v.Hits = []KeywordHit{}
	}
	out, err := bencode.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("dhtschema: encode value: %w", err)
	}
	if len(out) > MaxValueBytes {
		return nil, fmt.Errorf("dhtschema: encoded value %d bytes exceeds BEP-44 cap %d",
			len(out), MaxValueBytes)
	}
	return out, nil
}

// DecodeValue parses a bencoded KeywordValue retrieved from the DHT. This is
// one of the only places the package ingests untrusted remote bytes: a
// BEP-44-conforming node stores at most MaxValueBytes, but a hostile node can
// return up to the UDP datagram limit (~64 KiB). The over-cap check runs
// BEFORE bencode.Unmarshal so an oversized value cannot drive a large Hits
// allocation. The cap is inclusive — a payload of exactly MaxValueBytes
// decodes. An empty payload is an error.
func DecodeValue(payload []byte) (KeywordValue, error) {
	if len(payload) == 0 {
		return KeywordValue{}, errors.New("dhtschema: empty value")
	}
	if len(payload) > MaxValueBytes {
		return KeywordValue{}, fmt.Errorf("dhtschema: value %d bytes exceeds BEP-44 cap %d",
			len(payload), MaxValueBytes)
	}
	var v KeywordValue
	if err := bencode.Unmarshal(payload, &v); err != nil {
		return v, fmt.Errorf("dhtschema: decode value: %w", err)
	}
	return v, nil
}

// EstimateValueSize returns how many bytes the bencoded form of v will take at
// publish time. The publisher's eviction loop uses it to keep an entry under
// MaxValueBytes. EncodeValue overwrites a zero Ts with a live 10-digit
// timestamp (~16 bytes) right before the put; if the estimate marshalled with
// Ts==0 ("tsi0e", 7 bytes) the eviction loop could believe a near-cap entry
// fits while the live encode pushes it over the cap, making the keyword
// unpublishable. So a representative non-zero Ts is stamped here to account for
// the live timestamp width.
func EstimateValueSize(v KeywordValue) int {
	if v.Ts == 0 {
		v.Ts = time.Now().Unix()
	}
	out, err := bencode.Marshal(v)
	if err != nil {
		// A marshal failure would be a programmer bug — every field is
		// bencode-friendly — so assume the worst and force eviction.
		return MaxValueBytes + 1
	}
	return len(out)
}

// SaltForKeyword returns the byte salt used for a (publisher, keyword) DHT
// target. It is the keyword bytes VERBATIM — it does NOT lowercase and never
// truncates. Callers pass already-lowercased tokenizer output (contracts/token
// output is lowercased), so the salt stays byte-identical across every
// implementation. Errors on an empty keyword or one exceeding MaxSaltBytes.
func SaltForKeyword(keyword string) ([]byte, error) {
	if keyword == "" {
		return nil, errors.New("dhtschema: empty keyword")
	}
	salt := []byte(keyword)
	if len(salt) > MaxSaltBytes {
		return nil, fmt.Errorf("dhtschema: keyword salt %d bytes exceeds BEP-44 cap %d",
			len(salt), MaxSaltBytes)
	}
	return salt, nil
}

// SaltForShard returns the salt for shard N of a keyword. Shard 0 uses the
// bare keyword so existing readers still find it; shards 1+ append "#<n>".
// Reserved for the future multi-shard scheme described on KeywordValue.More;
// the v1 publish/lookup paths only ever use shard 0.
func SaltForShard(keyword string, shard int) ([]byte, error) {
	if shard == 0 {
		return SaltForKeyword(keyword)
	}
	return SaltForKeyword(fmt.Sprintf("%s#%d", keyword, shard))
}
