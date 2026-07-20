package dhtindex

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/exts/getput"
	"github.com/anacrolix/dht/v2/traversal"
	"github.com/anacrolix/torrent/bencode"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// Putter writes a KeywordValue to the DHT under the publisher's (pubkey,
// keyword) target and signs it per BEP-44. Getter reads one back under a
// specific (pubkey, salt) pair. They live beneath the RecordBackend seam so
// every backend shares the same wire path and the same fail-closed guard.
type Putter interface {
	Put(ctx context.Context, salt []byte, value dhtschema.KeywordValue) error
	// PublicKey is the publisher pubkey the Putter signs as.
	PublicKey() [32]byte
}

// Getter reads a signed KeywordValue. The signature is verified inside the
// anacrolix get path against the requested pubkey.
type Getter interface {
	Get(ctx context.Context, pubkey [32]byte, salt []byte) (dhtschema.KeywordValue, error)
}

// nextSeq returns seq+1, clamped at math.MaxInt64. BEP-44 requires strictly
// monotonic sequence numbers; the clamp keeps the closure signature total
// (seqToPut returns bep44.Put with no error path) and turns an otherwise-
// undefined int64 wrap into graceful degradation. Unreachable in practice: a
// publisher would have to push 2^63 updates to reach it.
func nextSeq(seq int64) int64 {
	if seq >= math.MaxInt64 {
		return math.MaxInt64
	}
	return seq + 1
}

// checkPutStats asserts a getput.Put traversal actually reached at least one
// DHT node. getput.Put returns a nil error even when the get-traversal reached
// zero nodes (a cold routing table, a transient partition, or a value every
// peer rejected) — in that case the BEP-44 item never lands, so the put must
// FAIL CLOSED instead of recording success for an item nobody can fetch. It is
// shared across every put path (keyword, BEP-46 pointer, and the Slice-12
// Aggregate paths) so the guard cannot drift; `what` names the path for the
// error message. Failing closed makes the publisher call MarkFailed (NOT
// advance LastPublished), so the 55m throttle does not suppress the retry of
// an undiscoverable keyword.
func checkPutStats(stats *traversal.Stats, what string) error {
	if stats == nil || stats.NumResponses == 0 {
		return fmt.Errorf("dhtindex: %s reached zero DHT nodes", what)
	}
	return nil
}

// AnacrolixPutter is the production Putter, backed by an anacrolix *dht.Server.
type AnacrolixPutter struct {
	server  *dht.Server
	private ed25519.PrivateKey
	public  [32]byte
}

// NewAnacrolixPutter wires a Putter against a live DHT server. The private key
// signs every put.
func NewAnacrolixPutter(server *dht.Server, priv ed25519.PrivateKey) (*AnacrolixPutter, error) {
	if server == nil {
		return nil, errors.New("dhtindex: nil DHT server")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("dhtindex: bad private key size %d", len(priv))
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok || len(pub) != 32 {
		return nil, errors.New("dhtindex: cannot derive public key")
	}
	var pubArr [32]byte
	copy(pubArr[:], pub)
	return &AnacrolixPutter{server: server, private: priv, public: pubArr}, nil
}

// PublicKey returns the publisher's public key.
func (a *AnacrolixPutter) PublicKey() [32]byte { return a.public }

// Put encodes the value, computes SHA1(pubkey||salt), and publishes to the
// closest DHT nodes via getput.Put. The value is re-decoded to an interface{}
// before signing so bep44.Put.Sign re-marshals byte-identically to what
// EncodeValue produced (the contracts/dhtschema re-marshal-identity guarantee).
// The put fails closed on a zero-node traversal.
func (a *AnacrolixPutter) Put(ctx context.Context, salt []byte, value dhtschema.KeywordValue) error {
	encoded, err := dhtschema.EncodeValue(value)
	if err != nil {
		return err
	}
	var v interface{}
	if err := bencode.Unmarshal(encoded, &v); err != nil {
		return fmt.Errorf("dhtindex: re-decode for put: %w", err)
	}
	target := bep44.MakeMutableTarget(a.public, salt)
	pubArr := a.public
	seqToPut := func(seq int64) bep44.Put {
		put := bep44.Put{V: v, K: &pubArr, Salt: salt, Seq: nextSeq(seq)}
		put.Sign(a.private)
		return put
	}
	stats, err := getput.Put(ctx, target, a.server, salt, seqToPut)
	if err != nil {
		return fmt.Errorf("dhtindex: put traversal: %w", err)
	}
	return checkPutStats(stats, "put")
}

// AnacrolixGetter is the production Getter, backed by an anacrolix *dht.Server.
// It needs no private key.
type AnacrolixGetter struct {
	server *dht.Server
}

// NewAnacrolixGetter wires a Getter against a live DHT server.
func NewAnacrolixGetter(server *dht.Server) (*AnacrolixGetter, error) {
	if server == nil {
		return nil, errors.New("dhtindex: nil DHT server")
	}
	return &AnacrolixGetter{server: server}, nil
}

// Get computes the SHA1(pubkey||salt) target, runs the BEP-44 get traversal,
// and decodes the highest-seq response. Signature verification happens inside
// the anacrolix get path; DecodeValue still re-applies the ≤1000 pre-unmarshal
// cap against a non-conforming node.
func (a *AnacrolixGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (dhtschema.KeywordValue, error) {
	target := bep44.MakeMutableTarget(pubkey, salt)
	res, _, err := getput.Get(ctx, target, a.server, nil, salt)
	if err != nil {
		return dhtschema.KeywordValue{}, fmt.Errorf("dhtindex: get %x: %w", target, err)
	}
	return dhtschema.DecodeValue([]byte(res.V))
}

// bep46Pointer is the typed shape of a BEP-46 mutable-item pointer value. A
// struct gives deterministic bencode output. TS (the publisher's wall-clock
// unix seconds at put time) is omitempty so pre-ts publishers round-trip as
// {"ih":...} without a ts:0 entry; decoders that don't know ts ignore it.
type bep46Pointer struct {
	IH []byte `bencode:"ih"`
	TS int64  `bencode:"ts,omitempty"`
}

// PointerInfo is the decoded BEP-46 pointer: the 20-byte infohash plus the
// publisher-asserted timestamp (zero when the publisher pre-dates the field).
type PointerInfo struct {
	InfoHash [20]byte
	TS       int64
}

// PutInfohashPointer publishes a BEP-46-style mutable item {"ih":<20B>,
// "ts":<unix>} under salt. This is the companion-index advertisement primitive
// consumed by Slice 10 (the salt is typically the well-known
// "_sn_content_index"). It fails closed via the shared checkPutStats.
func (a *AnacrolixPutter) PutInfohashPointer(ctx context.Context, salt []byte, infohash [20]byte) error {
	if len(salt) == 0 {
		return errors.New("dhtindex: empty salt")
	}
	if len(salt) > dhtschema.MaxSaltBytes {
		return fmt.Errorf("dhtindex: salt %d bytes exceeds BEP-44 cap of %d", len(salt), dhtschema.MaxSaltBytes)
	}
	v := bep46Pointer{IH: infohash[:], TS: time.Now().Unix()}
	encoded, err := bencode.Marshal(v)
	if err != nil {
		return fmt.Errorf("dhtindex: marshal pointer: %w", err)
	}
	var decoded interface{}
	if err := bencode.Unmarshal(encoded, &decoded); err != nil {
		return fmt.Errorf("dhtindex: re-decode pointer: %w", err)
	}
	target := bep44.MakeMutableTarget(a.public, salt)
	pubArr := a.public
	seqToPut := func(seq int64) bep44.Put {
		put := bep44.Put{V: decoded, K: &pubArr, Salt: salt, Seq: nextSeq(seq)}
		put.Sign(a.private)
		return put
	}
	stats, err := getput.Put(ctx, target, a.server, salt, seqToPut)
	if err != nil {
		return fmt.Errorf("dhtindex: put pointer: %w", err)
	}
	return checkPutStats(stats, "pointer put")
}

// GetInfohashPointerInfo fetches a BEP-46 pointer under (pubkey, salt),
// returning the infohash and the publisher-asserted timestamp.
func (a *AnacrolixGetter) GetInfohashPointerInfo(ctx context.Context, pubkey [32]byte, salt []byte) (PointerInfo, error) {
	var zero PointerInfo
	if len(salt) == 0 {
		return zero, errors.New("dhtindex: empty salt")
	}
	target := bep44.MakeMutableTarget(pubkey, salt)
	res, _, err := getput.Get(ctx, target, a.server, nil, salt)
	if err != nil {
		return zero, fmt.Errorf("dhtindex: get pointer %x: %w", target, err)
	}
	return decodePointerValue([]byte(res.V))
}

// GetInfohashPointer returns just the infohash from a BEP-46 pointer.
func (a *AnacrolixGetter) GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error) {
	info, err := a.GetInfohashPointerInfo(ctx, pubkey, salt)
	if err != nil {
		return [20]byte{}, err
	}
	return info.InfoHash, nil
}

// decodePointerValue validates and decodes a remote-supplied BEP-46 pointer.
// The value is signature-verified by the get path but is still untrusted
// publisher input, so it is bounded by the same ≤MaxValueBytes cap BEFORE
// unmarshal that the keyword decoder applies.
func decodePointerValue(raw []byte) (PointerInfo, error) {
	var zero PointerInfo
	if len(raw) > dhtschema.MaxValueBytes {
		return zero, fmt.Errorf("dhtindex: pointer value %d bytes exceeds BEP-44 cap of %d", len(raw), dhtschema.MaxValueBytes)
	}
	var v bep46Pointer
	// Bound the parsed string length: the outer len(raw) cap above does NOT stop
	// alloc amplification, because anacrolix bencode.Unmarshal leaves MaxStrLen at
	// its ~128 MiB default and does make([]byte, declaredLen) BEFORE reading — so
	// a tiny signed value like `d2:ih134217727:e` (declares a 128 MiB ih string)
	// passes the 1000-byte cap yet forces a ~128 MiB transient allocation. A
	// bencoded string can't exceed its payload, so MaxStrLen = len(raw) rejects
	// only impossible/hostile lengths. Same defense as contracts/dhtschema (the
	// keyword-value decoder) and contracts/ltepwire; this pointer decoder is a
	// sibling site that shipped without the bound.
	d := bencode.NewDecoder(bytes.NewReader(raw))
	if n := int64(len(raw)); n > 0 {
		d.MaxStrLen = n
	}
	if err := d.Decode(&v); err != nil {
		return zero, fmt.Errorf("dhtindex: decode pointer: %w", err)
	}
	if len(v.IH) != 20 {
		return zero, fmt.Errorf("dhtindex: pointer ih has %d bytes, want 20", len(v.IH))
	}
	out := PointerInfo{TS: v.TS}
	copy(out.InfoHash[:], v.IH)
	return out, nil
}
