package dhtindex

import (
	"errors"
	"fmt"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// MaxSaltBytes is BEP-44's hard cap on the salt field. Our keyword
// strings must fit under this once UTF-8 encoded; if not, we drop
// the keyword rather than trying to truncate (truncation could
// produce collisions across distinct keywords).
const MaxSaltBytes = 64

// MaxValueBytes is the BEP-44 hard cap on the bencoded `v` field
// of a mutable item. The publisher rejects any KeywordValue whose
// encoded form exceeds this size; the manifest splits across shards
// to keep individual values under the cap.
const MaxValueBytes = 1000

// KeywordValue is the bencoded payload stored at the DHT target
// computed from SHA1(publisher_pubkey || salt). It is the unit of
// publication for one (publisher, keyword) pair.
//
// Field names are deliberately short so the bencoded form stays
// small enough to fit ~25-40 hits per 1000-byte cap. Long names go
// in the local index, not the DHT.
type KeywordValue struct {
	// Ts is the unix timestamp at which this snapshot was generated.
	// Mostly informational; the BEP-44 sequence number is what
	// actually orders updates.
	Ts int64 `bencode:"ts"`

	// Hits is the list of torrent hits this publisher claims for
	// the keyword.
	Hits []KeywordHit `bencode:"hits"`

	// More is 1 if there are additional shards beyond this one,
	// stored at salts of the form "<keyword>#1", "<keyword>#2", …
	// Searchers fetch shard 0 first, then fan out to the rest.
	More int `bencode:"more,omitempty"`

	// NextPubKey is an optional 32-byte ed25519 public key, signed
	// by the current publisher's private key as part of the BEP-44
	// mutable item. It points to the publisher's "next" key in a
	// key-rotation chain, mirroring Tor v3's time-period-key chain.
	// Subscribers that see this field on a known publisher can
	// start following the new key automatically while still
	// trusting the rotation because it rode the current key's
	// signature.
	//
	// v1.0.0 ships the field on the wire but does NOT rotate —
	// NextPubKey is always empty in v1 puts. The rotation logic is
	// scheduled for v1.1. Having the field in the v1 schema means
	// future clients don't need a format bump to start using it.
	// See the "Privacy and threat model" section of
	// docs/08-operations.md for the motivation.
	NextPubKey []byte `bencode:"next_pk,omitempty"`
}

// KeywordHit is one entry in the Hits list. The field names mirror
// the sn_search wire format from M3 so that consumers can use the
// same hit type internally.
type KeywordHit struct {
	IH []byte `bencode:"ih"`           // 20-byte SHA-1 infohash
	N  string `bencode:"n,omitempty"`  // short torrent name
	S  int    `bencode:"s,omitempty"`  // seeders count (last seen)
	F  int    `bencode:"f,omitempty"`  // file count
	Sz int64  `bencode:"sz,omitempty"` // size in bytes
}

// EncodeValue serialises a KeywordValue. It also fills in Ts if
// the caller left it zero so the DHT entry always has a fresh
// timestamp.
func EncodeValue(v KeywordValue) ([]byte, error) {
	if v.Ts == 0 {
		v.Ts = time.Now().Unix()
	}
	if v.Hits == nil {
		v.Hits = []KeywordHit{}
	}
	out, err := bencode.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("dhtindex: encode value: %w", err)
	}
	if len(out) > MaxValueBytes {
		return nil, fmt.Errorf("dhtindex: encoded value %d bytes exceeds BEP-44 cap %d",
			len(out), MaxValueBytes)
	}
	return out, nil
}

// DecodeValue parses a bencoded KeywordValue retrieved from the DHT.
func DecodeValue(payload []byte) (KeywordValue, error) {
	if len(payload) == 0 {
		return KeywordValue{}, errors.New("dhtindex: empty value")
	}
	var v KeywordValue
	if err := bencode.Unmarshal(payload, &v); err != nil {
		return v, fmt.Errorf("dhtindex: decode value: %w", err)
	}
	return v, nil
}

// EstimateValueSize returns how many bytes the bencoded form of v
// will take. Used by the manifest to decide when to spill into a
// new shard before hitting the MaxValueBytes ceiling.
func EstimateValueSize(v KeywordValue) int {
	out, err := bencode.Marshal(v)
	if err != nil {
		// On the rare encoding failure (which would be a programmer
		// bug — every field is bencode-friendly), assume the worst.
		return MaxValueBytes + 1
	}
	return len(out)
}

// SaltForKeyword returns the byte salt used for a (publisher, keyword)
// DHT target. It is the lowercased UTF-8 form of the keyword. Returns
// an error if the salt would exceed MaxSaltBytes.
func SaltForKeyword(keyword string) ([]byte, error) {
	if keyword == "" {
		return nil, errors.New("dhtindex: empty keyword")
	}
	salt := []byte(keyword)
	if len(salt) > MaxSaltBytes {
		return nil, fmt.Errorf("dhtindex: keyword salt %d bytes exceeds BEP-44 cap %d",
			len(salt), MaxSaltBytes)
	}
	return salt, nil
}

// SaltForShard returns the salt for shard N of a keyword. Shard 0
// uses the bare keyword (so existing readers find it), shards 1+
// append "#<n>".
func SaltForShard(keyword string, shard int) ([]byte, error) {
	if shard == 0 {
		return SaltForKeyword(keyword)
	}
	return SaltForKeyword(fmt.Sprintf("%s#%d", keyword, shard))
}
