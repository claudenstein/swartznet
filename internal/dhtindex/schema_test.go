package dhtindex_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	orig := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{IH: bytes.Repeat([]byte{0x01}, 20), N: "ubuntu", S: 100, F: 12, Sz: 6 << 30},
			{IH: bytes.Repeat([]byte{0x02}, 20), N: "ubuntu desktop", S: 50, F: 4, Sz: 4 << 30},
		},
	}
	encoded, err := dhtindex.EncodeValue(orig)
	if err != nil {
		t.Fatalf("EncodeValue: %v", err)
	}
	if len(encoded) > dhtindex.MaxValueBytes {
		t.Errorf("encoded size %d exceeds cap %d", len(encoded), dhtindex.MaxValueBytes)
	}

	decoded, err := dhtindex.DecodeValue(encoded)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if decoded.Ts == 0 {
		t.Error("decoded.Ts is zero; EncodeValue should auto-fill it")
	}
	if len(decoded.Hits) != 2 {
		t.Fatalf("decoded.Hits len = %d, want 2", len(decoded.Hits))
	}
	if !bytes.Equal(decoded.Hits[0].IH, orig.Hits[0].IH) {
		t.Errorf("first hit IH mismatch")
	}
	if decoded.Hits[1].N != "ubuntu desktop" {
		t.Errorf("second hit N = %q, want 'ubuntu desktop'", decoded.Hits[1].N)
	}
}

func TestEncodeRejectsOversized(t *testing.T) {
	t.Parallel()
	// Stuff in 100 hits with maximum-sized names → guaranteed to
	// exceed the 1000-byte cap.
	v := dhtindex.KeywordValue{}
	for i := 0; i < 100; i++ {
		v.Hits = append(v.Hits, dhtindex.KeywordHit{
			IH: bytes.Repeat([]byte{byte(i)}, 20),
			N:  strings.Repeat("x", 60),
		})
	}
	_, err := dhtindex.EncodeValue(v)
	if err == nil {
		t.Fatal("expected error for oversized value")
	}
	if !strings.Contains(err.Error(), "exceeds BEP-44 cap") {
		t.Errorf("error = %q, want it to mention 'exceeds BEP-44 cap'", err.Error())
	}
}

func TestDecodeEmpty(t *testing.T) {
	t.Parallel()
	if _, err := dhtindex.DecodeValue(nil); err == nil {
		t.Error("expected error for nil payload")
	}
}

func TestSaltForKeyword(t *testing.T) {
	t.Parallel()
	salt, err := dhtindex.SaltForKeyword("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if string(salt) != "ubuntu" {
		t.Errorf("salt = %q, want 'ubuntu'", salt)
	}

	if _, err := dhtindex.SaltForKeyword(""); err == nil {
		t.Error("expected error for empty keyword")
	}
	if _, err := dhtindex.SaltForKeyword(strings.Repeat("x", dhtindex.MaxSaltBytes+1)); err == nil {
		t.Error("expected error for oversized keyword")
	}
}

func TestSaltForShardZero(t *testing.T) {
	t.Parallel()
	zero, _ := dhtindex.SaltForShard("ubuntu", 0)
	first, _ := dhtindex.SaltForShard("ubuntu", 1)
	second, _ := dhtindex.SaltForShard("ubuntu", 2)
	if string(zero) != "ubuntu" {
		t.Errorf("shard 0 salt = %q, want 'ubuntu'", zero)
	}
	if string(first) != "ubuntu#1" {
		t.Errorf("shard 1 salt = %q, want 'ubuntu#1'", first)
	}
	if string(second) != "ubuntu#2" {
		t.Errorf("shard 2 salt = %q, want 'ubuntu#2'", second)
	}
}

func TestEstimateValueSize(t *testing.T) {
	t.Parallel()
	v := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{IH: bytes.Repeat([]byte{0x01}, 20), N: "test"},
		},
	}
	size := dhtindex.EstimateValueSize(v)
	if size <= 0 || size > dhtindex.MaxValueBytes {
		t.Errorf("EstimateValueSize = %d, want a positive value below the cap", size)
	}
}

// TestMemoryPutterGetterRoundTrip exercises the in-memory Putter +
// Getter pair that the publisher and lookup tests will use.
func TestMemoryPutterGetterRoundTrip(t *testing.T) {
	t.Parallel()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store := dhtindex.NewMemoryPutterGetter(priv)

	salt, _ := dhtindex.SaltForKeyword("ubuntu")
	value := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{IH: bytes.Repeat([]byte{0xab}, 20), N: "Ubuntu 24.04", S: 100},
		},
	}
	if err := store.Put(context.Background(), salt, value); err != nil {
		t.Fatalf("Put: %v", err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	var pubArr [32]byte
	copy(pubArr[:], pub)
	got, err := store.Get(context.Background(), pubArr, salt)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Hits) != 1 || string(got.Hits[0].N) != "Ubuntu 24.04" {
		t.Errorf("got = %+v, want one hit named 'Ubuntu 24.04'", got)
	}
}

func TestMemoryGetterMissingTargetErrors(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := dhtindex.NewMemoryPutterGetter(priv)
	var pubArr [32]byte
	pub := priv.Public().(ed25519.PublicKey)
	copy(pubArr[:], pub)
	_, err := store.Get(context.Background(), pubArr, []byte("never-published"))
	if err == nil {
		t.Error("expected error for missing target")
	}
}
