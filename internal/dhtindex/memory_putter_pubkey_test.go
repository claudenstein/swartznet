package dhtindex_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestMemoryPutterGetterPubKeyMatchesPriv covers the PubKey
// accessor: the [32]byte returned MUST match the public key
// derived from the private key the store was constructed with.
// Tests rely on this to query under the same key the store
// signed Put records under.
func TestMemoryPutterGetterPubKeyMatchesPriv(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := dhtindex.NewMemoryPutterGetter(priv)

	got := m.PubKey()
	if string(got[:]) != string(pub) {
		t.Errorf("PubKey = %x, want %x", got[:8], pub[:8])
	}
}

// TestMemoryPutterGetterPubKeyZeroForNilPriv — constructing
// with nil priv should leave PubKey at its zero value, not
// panic on the type assertion or copy step.
func TestMemoryPutterGetterPubKeyZeroForNilPriv(t *testing.T) {
	t.Parallel()
	m := dhtindex.NewMemoryPutterGetter(nil)
	if pk := m.PubKey(); pk != ([32]byte{}) {
		t.Errorf("PubKey for nil priv = %x, want zero", pk[:8])
	}
}
