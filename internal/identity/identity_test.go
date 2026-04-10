package identity_test

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/identity"
)

func TestLoadOrCreateGeneratesNewKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "id.key")

	id, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(id.PrivateKey) != ed25519.PrivateKeySize {
		t.Errorf("private key size = %d, want %d", len(id.PrivateKey), ed25519.PrivateKeySize)
	}
	if len(id.PublicKey) != ed25519.PublicKeySize {
		t.Errorf("public key size = %d, want %d", len(id.PublicKey), ed25519.PublicKeySize)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if stat.Mode().Perm() != identity.KeyFilePerms {
		t.Errorf("file mode = %#o, want %#o", stat.Mode().Perm(), identity.KeyFilePerms)
	}
}

func TestLoadOrCreateRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "id.key")

	first, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	// The second call should see the same keypair the first call wrote.
	if string(first.PrivateKey) != string(second.PrivateKey) {
		t.Errorf("second LoadOrCreate produced a different private key")
	}
	if string(first.PublicKey) != string(second.PublicKey) {
		t.Errorf("second LoadOrCreate produced a different public key")
	}
	if first.PublicKeyHex() != second.PublicKeyHex() {
		t.Errorf("public key hex mismatch")
	}
	if len(first.PublicKeyHex()) != 64 {
		t.Errorf("PublicKeyHex len = %d, want 64", len(first.PublicKeyHex()))
	}
}

func TestLoadOrCreateRejectsInsecurePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "id.key")

	// Create the file with mode 0644 — group/other readable.
	if err := os.WriteFile(path, make([]byte, ed25519.PrivateKeySize), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := identity.LoadOrCreate(path)
	if err == nil {
		t.Fatal("expected an error for insecure permissions")
	}
	if !strings.Contains(err.Error(), "insecure permissions") {
		t.Errorf("error = %q, want it to mention 'insecure permissions'", err.Error())
	}
}

func TestLoadOrCreateRejectsCorruptKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "id.key")
	// Write a too-short file with the right permissions.
	if err := os.WriteFile(path, []byte("not really a key"), identity.KeyFilePerms); err != nil {
		t.Fatal(err)
	}
	_, err := identity.LoadOrCreate(path)
	if err == nil {
		t.Fatal("expected an error for corrupt key file")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("error = %q, want it to mention 'corrupt'", err.Error())
	}
}

func TestLoadOrCreateRejectsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subdir := filepath.Join(dir, "id.key")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := identity.LoadOrCreate(subdir)
	if err == nil {
		t.Fatal("expected an error when path is a directory")
	}
}

func TestPublicKeyBytesShape(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "id.key")
	id, err := identity.LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	bytes := id.PublicKeyBytes()
	if len(bytes) != 32 {
		t.Errorf("PublicKeyBytes len = %d, want 32", len(bytes))
	}
	// And it must equal the slice form.
	for i := 0; i < 32; i++ {
		if bytes[i] != id.PublicKey[i] {
			t.Errorf("byte %d differs: %x vs %x", i, bytes[i], id.PublicKey[i])
		}
	}
}

func TestLoadOrCreateEmptyPathRejected(t *testing.T) {
	t.Parallel()
	if _, err := identity.LoadOrCreate(""); err == nil {
		t.Error("expected an error for empty path")
	} else if !errors.Is(err, errors.New("identity: path must not be empty")) {
		// errors.Is on a constructed error returns false unless the
		// underlying error chain matches; we just verify the message.
		if !strings.Contains(err.Error(), "must not be empty") {
			t.Errorf("error = %q, want it to mention 'must not be empty'", err.Error())
		}
	}
}
