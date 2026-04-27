package dhtindex_test

import (
	"context"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestAnacrolixGetterGetCancelledCtx covers AnacrolixGetter.Get's
// `getput.Get err → return wrapped err` arm. With an isolated DHT
// server (no peers, Passive) and a pre-cancelled context, the
// traversal aborts immediately and surfaces an error.
func TestAnacrolixGetterGetCancelledCtx(t *testing.T) {
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
	if _, err := get.Get(ctx, pub, []byte("salt")); err == nil {
		t.Error("Get with cancelled ctx should error from getput.Get")
	}
}
