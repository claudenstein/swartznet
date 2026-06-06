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
// encoded form exceeds this size. To stay under the cap the manifest
// evicts the oldest hit from an entry (Manifest.AddHit); multi-shard
// spill is reserved scaffolding (see KeywordValue.More) and is not
// yet wired into the publish/lookup paths.
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

	// More is reserved for a future multi-shard scheme: when set to
	// 1 it would signal additional shards stored at salts of the form
	// "<keyword>#1", "<keyword>#2", … (see SaltForShard), with
	// searchers fetching shard 0 first then fanning out.
	//
	// As of v1 this is NOT implemented: the publisher never sets More
	// and never writes shard 1+, and Lookup never follows it. Oversize
	// entries are bounded by evicting the oldest hit (Manifest.AddHit),
	// not by spilling into a new shard. The field ships on the wire
	// (omitted while zero) and SaltForShard stays exported so the
	// spill/redundancy scheme can be added without a format bump.
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
// This is one of the only places the package ingests untrusted remote
// bytes, so it mirrors the encode-side cap: a BEP-44-conforming node
// stores at most MaxValueBytes, but a non-conforming or malicious node
// can return up to the UDP datagram limit (~64 KiB). Reject anything
// over the cap before unmarshalling so a hostile value cannot drive an
// oversized Hits allocation.
func DecodeValue(payload []byte) (KeywordValue, error) {
	if len(payload) == 0 {
		return KeywordValue{}, errors.New("dhtindex: empty value")
	}
	if len(payload) > MaxValueBytes {
		return KeywordValue{}, fmt.Errorf("dhtindex: value %d bytes exceeds BEP-44 cap %d",
			len(payload), MaxValueBytes)
	}
	var v KeywordValue
	if err := bencode.Unmarshal(payload, &v); err != nil {
		return v, fmt.Errorf("dhtindex: decode value: %w", err)
	}
	return v, nil
}

// EstimateValueSize returns how many bytes the bencoded form of v
// will take at publish time. Used by the manifest's eviction loop to
// keep an entry under the MaxValueBytes ceiling.
//
// EncodeValue overwrites a zero Ts with time.Now().Unix() (a 10-digit
// value, "tsi1700000000e", ~16 bytes) right before the put. If the
// estimate marshalled with Ts==0 ("tsi0e", 7 bytes) the manifest could
// believe a near-cap entry fits while the live encode pushes it over
// MaxValueBytes, making EncodeValue fail and the keyword unpublishable.
// So we account for the live timestamp width here by stamping a
// representative non-zero Ts before marshalling.
func EstimateValueSize(v KeywordValue) int {
	if v.Ts == 0 {
		v.Ts = time.Now().Unix()
	}
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
//
// Reserved for the future multi-shard scheme described on
// KeywordValue.More; the v1 publish/lookup paths only use shard 0.
func SaltForShard(keyword string, shard int) ([]byte, error) {
	if shard == 0 {
		return SaltForKeyword(keyword)
	}
	return SaltForKeyword(fmt.Sprintf("%s#%d", keyword, shard))
}
