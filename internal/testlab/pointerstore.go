package testlab

import (
	"context"
	"errors"
	"sync"
)

// MemoryPointerStore is an in-process BEP-46 mutable-item store
// used by Layer-A testbed scenarios to exercise the F3
// companion pointer publish/lookup path without a real DHT.
// Each test creates one and hands Putter / Getter wrappers to
// companion.Publisher and companion.Subscriber.
//
// Unlike dhtindex.SharedMemoryStore (which carries Layer-D
// KeywordValue payloads), this store holds the minimal BEP-46
// pointer shape — 20-byte infohash keyed by (publisher pubkey,
// salt). Keeping the two stores separate mirrors production:
// dhtindex.AnacrolixPutter exposes Put for Layer D and
// PutInfohashPointer for BEP-46 as distinct methods.
type MemoryPointerStore struct {
	mu    sync.Mutex
	items map[pointerKey][20]byte
}

type pointerKey struct {
	pubkey [32]byte
	salt   string
}

// NewMemoryPointerStore constructs an empty pointer store.
func NewMemoryPointerStore() *MemoryPointerStore {
	return &MemoryPointerStore{
		items: make(map[pointerKey][20]byte),
	}
}

// PutterFor returns a companion.PointerPutter bound to the
// given publisher pubkey. Every call to
// PutInfohashPointer(ctx, salt, ih) stores ih in the shared
// map under (pub, salt).
func (s *MemoryPointerStore) PutterFor(pub [32]byte) *pointerPutter {
	return &pointerPutter{store: s, pub: pub}
}

// Getter returns a companion.PointerGetter that reads from
// the shared map.
func (s *MemoryPointerStore) Getter() *pointerGetter {
	return &pointerGetter{store: s}
}

// pointerPutter satisfies companion.PointerPutter (the
// interface is defined in the companion package as
// `PutInfohashPointer(ctx, salt, infohash [20]byte) error`).
type pointerPutter struct {
	store *MemoryPointerStore
	pub   [32]byte
}

func (p *pointerPutter) PutInfohashPointer(ctx context.Context, salt []byte, infohash [20]byte) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	p.store.items[pointerKey{pubkey: p.pub, salt: string(salt)}] = infohash
	return nil
}

// pointerGetter satisfies companion.PointerGetter (the
// interface is defined in the companion package as
// `GetInfohashPointer(ctx, pubkey [32]byte, salt []byte) ([20]byte, error)`).
type pointerGetter struct {
	store *MemoryPointerStore
}

func (g *pointerGetter) GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error) {
	g.store.mu.Lock()
	defer g.store.mu.Unlock()
	ih, ok := g.store.items[pointerKey{pubkey: pubkey, salt: string(salt)}]
	if !ok {
		return [20]byte{}, errors.New("testlab: pointer not found")
	}
	return ih, nil
}
