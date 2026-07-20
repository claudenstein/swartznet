package dhtindex

import (
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"errors"
	"sync"

	"github.com/swartznet/swartznet/contracts/dhtschema"
)

// This file holds in-process Putter/Getter fakes so the publisher worker and
// the lookup path can be unit-tested — and 2-node scenarios run — without a
// real DHT server. Production code never uses these directly; the target is
// spelled sha1.Sum(pub||salt) to mirror bep44.MakeMutableTarget.

// MemoryPutterGetter is a single-publisher in-memory Putter+Getter.
type MemoryPutterGetter struct {
	mu    sync.Mutex
	store map[[20]byte]storedItem
	pub   [32]byte
}

type storedItem struct {
	value dhtschema.KeywordValue
	seq   int64
}

// NewMemoryPutterGetter constructs an in-memory store. The private key fixes
// the publisher pubkey that Puts sign as (its bytes are never used for real
// signing — the in-memory Get path does not verify). A nil key leaves the
// pubkey zero.
func NewMemoryPutterGetter(priv ed25519.PrivateKey) *MemoryPutterGetter {
	m := &MemoryPutterGetter{store: make(map[[20]byte]storedItem)}
	if priv != nil {
		if pub, ok := priv.Public().(ed25519.PublicKey); ok {
			copy(m.pub[:], pub)
		}
	}
	return m
}

// PublicKey returns the publisher pubkey the store puts under.
func (m *MemoryPutterGetter) PublicKey() [32]byte { return m.pub }

// PubKey is a legacy alias for PublicKey.
func (m *MemoryPutterGetter) PubKey() [32]byte { return m.pub }

// Put stores a value under (pub, salt), enforcing the encode-side cap.
func (m *MemoryPutterGetter) Put(_ context.Context, salt []byte, value dhtschema.KeywordValue) error {
	if _, err := dhtschema.EncodeValue(value); err != nil {
		return err
	}
	target := sha1.Sum(append(append([]byte(nil), m.pub[:]...), salt...))
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.store[target]
	m.store[target] = storedItem{value: value, seq: prev.seq + 1}
	return nil
}

// Get fetches the value at (pubkey, salt).
func (m *MemoryPutterGetter) Get(_ context.Context, pubkey [32]byte, salt []byte) (dhtschema.KeywordValue, error) {
	target := sha1.Sum(append(append([]byte(nil), pubkey[:]...), salt...))
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.store[target]
	if !ok {
		return dhtschema.KeywordValue{}, errors.New("dhtindex: not found")
	}
	return item.value, nil
}

// SharedMemoryStore is a multi-publisher in-process map: it issues a Putter per
// publisher while every Getter reads the same view, so several in-process nodes
// can publish under their own keys and each node's Getter sees them all.
type SharedMemoryStore struct {
	mu    sync.Mutex
	store map[[20]byte]storedItem
}

// NewSharedMemoryStore constructs an empty shared store.
func NewSharedMemoryStore() *SharedMemoryStore {
	return &SharedMemoryStore{store: make(map[[20]byte]storedItem)}
}

// PutterFor returns a Putter that writes to the shared store under priv's key.
func (s *SharedMemoryStore) PutterFor(priv ed25519.PrivateKey) Putter {
	var pub [32]byte
	if p, ok := priv.Public().(ed25519.PublicKey); ok {
		copy(pub[:], p)
	}
	return &sharedPutter{store: s, pub: pub}
}

// Getter returns a Getter reading from the shared store.
func (s *SharedMemoryStore) Getter() Getter { return &sharedGetter{store: s} }

type sharedPutter struct {
	store *SharedMemoryStore
	pub   [32]byte
}

func (p *sharedPutter) PublicKey() [32]byte { return p.pub }

func (p *sharedPutter) Put(_ context.Context, salt []byte, value dhtschema.KeywordValue) error {
	if _, err := dhtschema.EncodeValue(value); err != nil {
		return err
	}
	target := sha1.Sum(append(append([]byte(nil), p.pub[:]...), salt...))
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	prev := p.store.store[target]
	p.store.store[target] = storedItem{value: value, seq: prev.seq + 1}
	return nil
}

type sharedGetter struct{ store *SharedMemoryStore }

func (g *sharedGetter) Get(_ context.Context, pubkey [32]byte, salt []byte) (dhtschema.KeywordValue, error) {
	target := sha1.Sum(append(append([]byte(nil), pubkey[:]...), salt...))
	g.store.mu.Lock()
	defer g.store.mu.Unlock()
	item, ok := g.store.store[target]
	if !ok {
		return dhtschema.KeywordValue{}, errors.New("dhtindex: not found")
	}
	return item.value, nil
}
