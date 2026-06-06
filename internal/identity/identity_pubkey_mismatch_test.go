package identity_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/identity"
)

// TestLoadFromDiskRejectsMismatchedPublicHalf covers the
// re-derivation check in loadFromDisk. If the trailing 32 bytes of
// the key file (the public half) are corrupted independently of the
// seed, the loader must fail loud rather than return an Identity
// whose PublicKey does not correspond to its signing seed (which
// would surface much later as "signature does not verify").
func TestLoadFromDiskRejectsMismatchedPublicHalf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "id.key")

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, ed25519.PrivateKeySize)
	copy(raw, priv)
	// Flip a bit in the public half (bytes [32:64]); the seed stays
	// intact so the public half no longer matches the seed.
	raw[ed25519.SeedSize] ^= 0x01
	if err := os.WriteFile(path, raw, identity.KeyFilePerms); err != nil {
		t.Fatal(err)
	}

	_, err = identity.LoadOrCreate(path)
	if err == nil {
		t.Fatal("LoadOrCreate should reject a key whose public half does not match its seed")
	}
	if !strings.Contains(err.Error(), "public half does not match seed") {
		t.Errorf("error = %q, want it to mention 'public half does not match seed'", err.Error())
	}
}

// TestLoadFromDiskAcceptsValidKey is the positive control: a key
// whose public half is correctly derived from its seed must load
// cleanly, ensuring the new check does not reject genuine files.
func TestLoadFromDiskAcceptsValidKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "id.key")

	// Create via LoadOrCreate so the on-disk bytes are canonical, then
	// reload to exercise loadFromDisk's validation path.
	first, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("initial LoadOrCreate: %v", err)
	}
	second, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("reload LoadOrCreate: %v", err)
	}
	if !first.PublicKey.Equal(second.PublicKey) {
		t.Error("reloaded public key differs from the originally generated one")
	}
}
