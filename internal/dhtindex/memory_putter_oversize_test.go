package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestMemoryPutterRejectsOversizedValue covers the EncodeValue
// > MaxValueBytes error branch in MemoryPutterGetter.Put.
func TestMemoryPutterRejectsOversizedValue(t *testing.T) {
	t.Parallel()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mem := dhtindex.NewMemoryPutterGetter(priv)

	// One hit with a name that's > MaxValueBytes (1000 bytes).
	bigName := string(bytes.Repeat([]byte{'a'}, dhtindex.MaxValueBytes+200))
	value := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{IH: bytes.Repeat([]byte{1}, 20), N: bigName},
		},
	}

	if err := mem.Put(context.Background(), []byte("k"), value); err == nil {
		t.Error("Put with oversize value should error")
	}
}

// TestSharedMemoryStorePutterRejectsOversizedValue is the
// matching test for SharedMemoryStore.PutterFor's Put.
func TestSharedMemoryStorePutterRejectsOversizedValue(t *testing.T) {
	t.Parallel()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := dhtindex.NewSharedMemoryStore()
	put := store.PutterFor(priv)

	bigName := string(bytes.Repeat([]byte{'a'}, dhtindex.MaxValueBytes+200))
	value := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{IH: bytes.Repeat([]byte{1}, 20), N: bigName},
		},
	}

	if err := put.Put(context.Background(), []byte("k"), value); err == nil {
		t.Error("SharedMemoryStore Put with oversize value should error")
	}
}
