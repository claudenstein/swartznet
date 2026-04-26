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

// TestPutInfohashPointerDHTTraversalFails covers the
// `getput.Put(...)` error arm in PutInfohashPointer. With a
// pre-canceled context the put traversal aborts before
// contacting any peers, surfacing the wrapped "put pointer"
// error.
func TestPutInfohashPointerDHTTraversalFails(t *testing.T) {
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
	ih[0] = 0xab
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := put.PutInfohashPointer(ctx, []byte("salt"), ih); err == nil {
		t.Error("PutInfohashPointer with canceled ctx should error from getput.Put")
	}
}

// TestPutDHTTraversalFails covers AnacrolixPutter.Put's
// getput.Put error arm. Same canceled-context pattern as the
// pointer-side test.
func TestPutDHTTraversalFails(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	val := dhtindex.KeywordValue{
		Hits: []dhtindex.KeywordHit{
			{IH: []byte(strings.Repeat("\x01", 20)), N: "ubuntu"},
		},
	}
	if err := put.Put(ctx, []byte("salt"), val); err == nil {
		t.Error("Put with canceled ctx should error from getput.Put")
	}
}

// TestGetPPMIDHTTraversalFails covers AnacrolixGetter.GetPPMI's
// getput.Get error arm.
func TestGetPPMIDHTTraversalFails(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)
	get, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		t.Fatal(err)
	}
	var pub [32]byte
	pub[0] = 0xab
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := get.GetPPMI(ctx, pub); err == nil {
		t.Error("GetPPMI with canceled ctx should error from getput.Get")
	}
}

// TestPutPPMIDHTTraversalFails covers AnacrolixPutter.PutPPMI's
// getput.Put error arm.
func TestPutPPMIDHTTraversalFails(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	val := dhtindex.PPMIValue{
		IH: []byte(strings.Repeat("\x01", 20)),
	}
	if err := put.PutPPMI(ctx, val); err == nil {
		t.Error("PutPPMI with canceled ctx should error from getput.Put")
	}
}

// TestGetInfohashPointerDHTTraversalFails covers the
// `res, _, err := getput.Get(...); if err != nil { return ... }`
// arm. With an isolated DHT server (no peers, Passive mode)
// and a pre-canceled context, the traversal aborts immediately
// and surfaces a wrapped error.
func TestGetInfohashPointerDHTTraversalFails(t *testing.T) {
	t.Parallel()
	srv := newIsolatedDHTServer(t)
	get, err := dhtindex.NewAnacrolixGetter(srv)
	if err != nil {
		t.Fatal(err)
	}

	var pub [32]byte
	pub[0] = 0xab
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = get.GetInfohashPointer(ctx, pub, []byte("salt"))
	if err == nil {
		t.Error("GetInfohashPointer with canceled ctx should error from getput.Get")
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
