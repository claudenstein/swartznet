package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestAnacrolixPutEncodeError covers AnacrolixPutter.Put's
// `encoded, err := EncodeValue(value); if err != nil { return err }`
// arm. Pass a KeywordValue whose encoded form would exceed
// MaxValueBytes — EncodeValue rejects with the
// 'exceeds BEP-44 cap' error before any DHT traversal.
func TestAnacrolixPutEncodeError(t *testing.T) {
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

	// Build a hit whose Name alone overshoots the BEP-44 cap.
	bigName := string(bytes.Repeat([]byte{'a'}, dhtindex.MaxValueBytes+200))
	val := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{{
			IH: bytes.Repeat([]byte{0xab}, 20),
			N:  bigName,
		}},
	}
	if err := put.Put(context.Background(), []byte("salt"), val); err == nil {
		t.Error("Put with oversize KeywordValue should error from EncodeValue")
	}
}
