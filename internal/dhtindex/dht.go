package dhtindex

import (
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	"github.com/anacrolix/dht/v2/exts/getput"
	"github.com/anacrolix/torrent/bencode"
)

// Putter writes a KeywordValue to the DHT under the publisher's
// (pubkey, keyword) target. Implementations must sign the value with
// the publisher's private key per BEP-44.
type Putter interface {
	// Put stores the given value under salt. Returns an error if
	// the put traversal cannot reach a quorum of nodes or if the
	// value exceeds MaxValueBytes.
	Put(ctx context.Context, salt []byte, value KeywordValue) error
}

// Getter looks up a KeywordValue from the DHT under a specific
// (pubkey, salt) pair. Implementations must verify the BEP-44
// signature against the requested pubkey before returning a result.
type Getter interface {
	Get(ctx context.Context, pubkey [32]byte, salt []byte) (KeywordValue, error)
}

// AnacrolixPutter is the production Putter, backed by an
// anacrolix/dht/v2 *dht.Server. Construct with NewAnacrolixPutter
// after pulling the *dht.Server out of the engine via Engine.DHTServer.
type AnacrolixPutter struct {
	server  *dht.Server
	private ed25519.PrivateKey
	public  [32]byte
}

// NewAnacrolixPutter wires a Putter against a live anacrolix DHT
// server. The private key is used to sign every put.
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
	return &AnacrolixPutter{
		server:  server,
		private: priv,
		public:  pubArr,
	}, nil
}

// PublicKey returns the publisher's public key as a [32]byte. Useful
// for log output and for advertising "the pubkey at which my hits
// can be found" to other nodes.
func (a *AnacrolixPutter) PublicKey() [32]byte { return a.public }

// PutInfohashPointer publishes a BEP-46-style mutable item whose
// value is `{"ih": <20-byte infohash>}` under the given salt.
// This is the M11c publisher primitive used to advertise a
// companion content-index torrent at a deterministic
// (publisher_pubkey, salt) target. Subscribers fetch the
// pointer, read the infohash, and download the underlying
// torrent through normal BitTorrent.
//
// The salt is typically the well-known constant
// "_sn_content_index" (from the companion package) but the
// caller passes whatever bytes they like — anything ≤ 64 bytes
// is legal per BEP-44.
func (a *AnacrolixPutter) PutInfohashPointer(ctx context.Context, salt []byte, infohash [20]byte) error {
	if len(salt) == 0 {
		return errors.New("dhtindex: empty salt")
	}
	if len(salt) > 64 {
		return fmt.Errorf("dhtindex: salt %d bytes exceeds BEP-44 cap of 64", len(salt))
	}
	// Encode the value the same way subscribers will expect.
	// Using a typed struct with a single "ih" field gives us
	// stable bencode output regardless of map iteration order.
	v := bep46Pointer{IH: infohash[:]}
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
		put := bep44.Put{
			V:    decoded,
			K:    &pubArr,
			Salt: salt,
			Seq:  seq + 1,
		}
		put.Sign(a.private)
		return put
	}
	if _, err := getput.Put(ctx, target, a.server, salt, seqToPut); err != nil {
		return fmt.Errorf("dhtindex: put pointer: %w", err)
	}
	return nil
}

// bep46Pointer is the typed shape of a BEP-46 mutable item value.
// We use a struct so the bencoded output is deterministic and
// matches whatever the M11d subscriber side will decode.
type bep46Pointer struct {
	IH []byte `bencode:"ih"`
}

// GetInfohashPointer is the matching read-side helper. Returns
// the 20-byte infohash from a BEP-46 mutable item under
// (pubkey, salt). Used by the M11d subscriber to discover
// companion content indexes published by other nodes.
func (a *AnacrolixGetter) GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error) {
	var zero [20]byte
	if len(salt) == 0 {
		return zero, errors.New("dhtindex: empty salt")
	}
	target := bep44.MakeMutableTarget(pubkey, salt)
	res, _, err := getput.Get(ctx, target, a.server, nil, salt)
	if err != nil {
		return zero, fmt.Errorf("dhtindex: get pointer %x: %w", target, err)
	}
	var v bep46Pointer
	if err := bencode.Unmarshal([]byte(res.V), &v); err != nil {
		return zero, fmt.Errorf("dhtindex: decode pointer: %w", err)
	}
	if len(v.IH) != 20 {
		return zero, fmt.Errorf("dhtindex: pointer ih has %d bytes, want 20", len(v.IH))
	}
	var out [20]byte
	copy(out[:], v.IH)
	return out, nil
}

// Put implements Putter. It encodes the value, computes the BEP-44
// target from (publisher_pubkey, salt), and uses anacrolix's getput.Put
// to publish to the closest k DHT nodes. The provided ctx bounds the
// total operation time including the get-traversal that getput.Put
// performs internally to discover the current sequence number.
func (a *AnacrolixPutter) Put(ctx context.Context, salt []byte, value KeywordValue) error {
	encoded, err := EncodeValue(value)
	if err != nil {
		return err
	}
	// We pre-decode the encoded bytes back into an interface{} so
	// the bep44.Put.Sign call uses bencode.MustMarshal on a value
	// that round-trips identically to what we just encoded.
	var v interface{}
	if err := bencode.Unmarshal(encoded, &v); err != nil {
		return fmt.Errorf("dhtindex: re-decode for put: %w", err)
	}

	target := bep44.MakeMutableTarget(a.public, salt)
	pubArr := a.public
	seqToPut := func(seq int64) bep44.Put {
		put := bep44.Put{
			V:    v,
			K:    &pubArr,
			Salt: salt,
			Seq:  seq + 1,
		}
		put.Sign(a.private)
		return put
	}
	if _, err := getput.Put(ctx, target, a.server, salt, seqToPut); err != nil {
		return fmt.Errorf("dhtindex: put traversal: %w", err)
	}
	return nil
}

// AnacrolixGetter is the production Getter backed by an anacrolix
// *dht.Server. The Getter does not need a private key.
type AnacrolixGetter struct {
	server *dht.Server
}

// NewAnacrolixGetter wires a Getter against a live anacrolix DHT
// server.
func NewAnacrolixGetter(server *dht.Server) (*AnacrolixGetter, error) {
	if server == nil {
		return nil, errors.New("dhtindex: nil DHT server")
	}
	return &AnacrolixGetter{server: server}, nil
}

// Get implements Getter. It computes the SHA1(pubkey || salt) target,
// runs the BEP-44 get traversal, decodes the highest-seq response,
// and returns the parsed KeywordValue. Signature verification is
// performed inside the anacrolix get path.
func (a *AnacrolixGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (KeywordValue, error) {
	target := bep44.MakeMutableTarget(pubkey, salt)
	res, _, err := getput.Get(ctx, target, a.server, nil, salt)
	if err != nil {
		return KeywordValue{}, fmt.Errorf("dhtindex: get %x: %w", target, err)
	}
	return DecodeValue([]byte(res.V))
}

// MemoryPutterGetter is an in-process Putter+Getter backed by a
// concurrent map. It exists so the publisher worker (M4d) and the
// lookup path (M4e) can be unit-tested without spinning up a real
// DHT server. Production code should never use this directly.
type MemoryPutterGetter struct {
	mu    sync.Mutex
	store map[[20]byte]storedItem
	priv  ed25519.PrivateKey
	pub   [32]byte
}

type storedItem struct {
	pubkey [32]byte
	salt   []byte
	value  KeywordValue
	seq    int64
	stored time.Time
}

// NewMemoryPutterGetter constructs an in-memory store. The provided
// private key is used to sign every Put so the on-disk seq numbers
// behave the same way they would in production. Pass nil to skip
// signing (the test still works because the in-memory Get path does
// not verify signatures).
func NewMemoryPutterGetter(priv ed25519.PrivateKey) *MemoryPutterGetter {
	m := &MemoryPutterGetter{
		store: make(map[[20]byte]storedItem),
	}
	if priv != nil {
		m.priv = priv
		if pub, ok := priv.Public().(ed25519.PublicKey); ok {
			copy(m.pub[:], pub)
		}
	}
	return m
}

// Put stores a value under the (pub, salt) target.
func (m *MemoryPutterGetter) Put(ctx context.Context, salt []byte, value KeywordValue) error {
	if _, err := EncodeValue(value); err != nil {
		return err
	}
	target := sha1.Sum(append(m.pub[:], salt...))
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.store[target]
	m.store[target] = storedItem{
		pubkey: m.pub,
		salt:   append([]byte(nil), salt...),
		value:  value,
		seq:    prev.seq + 1,
		stored: time.Now(),
	}
	return nil
}

// Get fetches the value at (pubkey, salt) from the in-memory store.
func (m *MemoryPutterGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (KeywordValue, error) {
	target := sha1.Sum(append(pubkey[:], salt...))
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.store[target]
	if !ok {
		return KeywordValue{}, errors.New("dhtindex: not found")
	}
	return item.value, nil
}

// Items returns a snapshot of every entry in the store, sorted by
// the time they were last updated. Used by tests to assert what got
// stored without poking at internal fields.
func (m *MemoryPutterGetter) Items() []KeywordValue {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]KeywordValue, 0, len(m.store))
	for _, it := range m.store {
		out = append(out, it.value)
	}
	return out
}

// SharedMemoryStore is a multi-publisher in-process key-value
// map for BEP-44-style mutable items. Unlike MemoryPutterGetter
// (one fixed publisher), a SharedMemoryStore can issue a Putter
// per publisher while sharing the same underlying storage, so
// multiple in-process nodes can publish under their own ed25519
// keys and every node's Getter reads from the same view.
//
// Used by the internal/testlab package to run Layer-D scenarios
// without a real DHT. Production code should never use this.
type SharedMemoryStore struct {
	mu    sync.Mutex
	store map[[20]byte]storedItem
}

// NewSharedMemoryStore constructs an empty shared store.
func NewSharedMemoryStore() *SharedMemoryStore {
	return &SharedMemoryStore{
		store: make(map[[20]byte]storedItem),
	}
}

// PutterFor returns a Putter that writes to the shared store
// under the given ed25519 private key. The key is captured at
// call time; later changes to the key slice have no effect.
func (s *SharedMemoryStore) PutterFor(priv ed25519.PrivateKey) Putter {
	var pub [32]byte
	if p, ok := priv.Public().(ed25519.PublicKey); ok {
		copy(pub[:], p)
	}
	return &sharedPutter{store: s, priv: priv, pub: pub}
}

// Getter returns a Getter that reads from the shared store.
// Multiple calls return independent handles but they all read
// from the same underlying map.
func (s *SharedMemoryStore) Getter() Getter {
	return &sharedGetter{store: s}
}

// Items returns every KeywordValue currently in the store, in
// unspecified order. Used by tests to assert what was published.
func (s *SharedMemoryStore) Items() []KeywordValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KeywordValue, 0, len(s.store))
	for _, it := range s.store {
		out = append(out, it.value)
	}
	return out
}

// sharedPutter is a Putter bound to one publisher key, writing
// to a SharedMemoryStore.
type sharedPutter struct {
	store *SharedMemoryStore
	priv  ed25519.PrivateKey
	pub   [32]byte
}

func (p *sharedPutter) Put(ctx context.Context, salt []byte, value KeywordValue) error {
	if _, err := EncodeValue(value); err != nil {
		return err
	}
	target := sha1.Sum(append(p.pub[:], salt...))
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	prev := p.store.store[target]
	p.store.store[target] = storedItem{
		pubkey: p.pub,
		salt:   append([]byte(nil), salt...),
		value:  value,
		seq:    prev.seq + 1,
		stored: time.Now(),
	}
	return nil
}

// sharedGetter is a Getter reading from a SharedMemoryStore.
// The Get path does not verify signatures — it's a test-only
// fake — so any caller can look up any (pubkey, salt) target.
type sharedGetter struct {
	store *SharedMemoryStore
}

func (g *sharedGetter) Get(ctx context.Context, pubkey [32]byte, salt []byte) (KeywordValue, error) {
	target := sha1.Sum(append(pubkey[:], salt...))
	g.store.mu.Lock()
	defer g.store.mu.Unlock()
	item, ok := g.store.store[target]
	if !ok {
		return KeywordValue{}, errors.New("dhtindex: not found")
	}
	return item.value, nil
}
