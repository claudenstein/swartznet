package dhtindex_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestPutInfohashPointerEmptySalt covers the empty-salt validation
// guard at the top of PutInfohashPointer. The check fires before
// any DHT traffic, so a Passive isolated server is fine.
func TestPutInfohashPointerEmptySalt(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	put, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		t.Fatal(err)
	}

	var ih [20]byte
	err = put.PutInfohashPointer(context.Background(), nil, ih)
	if err == nil {
		t.Error("PutInfohashPointer with empty salt should error")
	}
}

// TestPutInfohashPointerSaltTooLarge covers the BEP-44 64-byte
// salt cap.
func TestPutInfohashPointerSaltTooLarge(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	put, err := dhtindex.NewAnacrolixPutter(srv, priv)
	if err != nil {
		t.Fatal(err)
	}

	var ih [20]byte
	bigSalt := []byte(strings.Repeat("x", 65))
	err = put.PutInfohashPointer(context.Background(), bigSalt, ih)
	if err == nil {
		t.Error("PutInfohashPointer with >64-byte salt should error")
	}
}

// TestGetInfohashPointerEmptySalt covers the matching empty-salt
// guard on the getter side.
func TestGetInfohashPointerEmptySalt(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)
	get, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		t.Fatal(err)
	}

	var pub [32]byte
	pub[0] = 0xab
	_, err = get.GetInfohashPointer(context.Background(), pub, nil)
	if err == nil {
		t.Error("GetInfohashPointer with empty salt should error")
	}
}
