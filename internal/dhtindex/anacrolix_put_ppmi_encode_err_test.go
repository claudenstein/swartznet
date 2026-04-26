package dhtindex_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestAnacrolixPutPPMIEncodeError covers AnacrolixPutter.PutPPMI's
// `encoded, err := EncodePPMI(value); if err != nil { return err }`
// arm. Pass a PPMIValue with wrong-length IH so EncodePPMI's
// `len(v.IH) != 20` validation fires before any DHT traversal.
func TestAnacrolixPutPPMIEncodeError(t *testing.T) {
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

	// 19-byte IH → EncodePPMI rejects.
	bad := dhtindex.PPMIValue{IH: make([]byte, 19)}
	if err := put.PutPPMI(context.Background(), bad); err == nil {
		t.Error("PutPPMI with 19-byte IH should error from EncodePPMI")
	}
}
