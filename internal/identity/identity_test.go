package identity

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func keyPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "identity.key")
}

func TestCreateNewKey(t *testing.T) {
	path := keyPath(t)
	id, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != KeyFilePerms {
		t.Fatalf("mode = %#o, want %#o", st.Mode().Perm(), KeyFilePerms)
	}
	if st.Size() != ed25519.PrivateKeySize {
		t.Fatalf("size = %d, want %d", st.Size(), ed25519.PrivateKeySize)
	}
	if len(id.PrivateKey) != 64 || len(id.PublicKey) != 32 {
		t.Fatalf("key sizes: priv %d pub %d", len(id.PrivateKey), len(id.PublicKey))
	}
}

func TestRoundTrip(t *testing.T) {
	path := keyPath(t)
	created, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, false) // reload must not need the create arm
	if err != nil {
		t.Fatal(err)
	}
	if !created.PrivateKey.Equal(loaded.PrivateKey) || !created.PublicKey.Equal(loaded.PublicKey) {
		t.Fatal("reloaded keypair differs")
	}
	hexKey := loaded.PublicKeyHex()
	if len(hexKey) != 64 || hexKey != hex.EncodeToString(loaded.PublicKey) {
		t.Fatalf("PublicKeyHex = %q", hexKey)
	}
	if hexKey != strings.ToLower(hexKey) {
		t.Fatal("PublicKeyHex must be lowercase")
	}
}

func TestPublicKeyBytes(t *testing.T) {
	id, err := Load(keyPath(t), true)
	if err != nil {
		t.Fatal(err)
	}
	b := id.PublicKeyBytes()
	if string(b[:]) != string(id.PublicKey) {
		t.Fatal("PublicKeyBytes mismatch")
	}
}

func TestSignerRoundTrip(t *testing.T) {
	id, err := Load(keyPath(t), true)
	if err != nil {
		t.Fatal(err)
	}
	s := id.Signer()
	msg := []byte("swartznet test message")
	sig := s.Sign(msg)
	if !ed25519.Verify(id.PublicKey, msg, sig) {
		t.Fatal("signature does not verify under the identity pubkey")
	}
	if !s.Public().Equal(id.PublicKey) {
		t.Fatal("Signer.Public differs from Identity.PublicKey")
	}
}

func TestPermissionGate(t *testing.T) {
	for _, mode := range []os.FileMode{0o400, 0o440, 0o640, 0o644, 0o660, 0o700, 0o777} {
		t.Run(mode.String(), func(t *testing.T) {
			path := keyPath(t)
			if _, err := Load(path, true); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, true)
			if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
				t.Fatalf("mode %#o: err = %v, want insecure-permissions rejection", mode, err)
			}
			// The rejection must leave the file untouched: same bytes, same mode.
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("rejected load modified the key file")
			}
			st, _ := os.Stat(path)
			if st.Mode().Perm() != mode {
				t.Fatalf("rejected load changed mode to %#o", st.Mode().Perm())
			}
		})
	}
}

func TestPermissionGateExactStrings(t *testing.T) {
	path := keyPath(t)
	if _, err := Load(path, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, true)
	want := "has insecure permissions 0400, want 0600 (chmod or delete the file)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want substring %q", err, want)
	}
}

func TestDirectoryAtPath(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir, true)
	if err == nil || !strings.Contains(err.Error(), "is a directory, not a key file") {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyPath(t *testing.T) {
	_, err := Load("", true)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestWrongSizeFiles(t *testing.T) {
	for _, size := range []int{0, 31, 32, 33, 63, 65} {
		path := keyPath(t)
		if err := os.WriteFile(path, make([]byte, size), KeyFilePerms); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path, true)
		if err == nil || !strings.Contains(err.Error(), "corrupt") {
			t.Fatalf("size %d: err = %v, want corrupt rejection", size, err)
		}
		// Never truncated, extended, or rewritten.
		st, _ := os.Stat(path)
		if st.Size() != int64(size) {
			t.Fatalf("size %d: file resized to %d", size, st.Size())
		}
	}
}

func TestPubkeyMismatch(t *testing.T) {
	path := keyPath(t)
	if _, err := Load(path, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[ed25519.SeedSize] ^= 0x01 // flip one bit in the stored public half
	if err := os.WriteFile(path, raw, KeyFilePerms); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path, true)
	if err == nil || !strings.Contains(err.Error(), "public half does not match seed") {
		t.Fatalf("err = %v", err)
	}
}

func TestSeedConsistentFileLoads(t *testing.T) {
	// Any 64-byte file whose halves are consistent must load — positive control.
	path := keyPath(t)
	priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	if err := os.WriteFile(path, priv, KeyFilePerms); err != nil {
		t.Fatal(err)
	}
	id, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !id.PrivateKey.Equal(priv) {
		t.Fatal("loaded key differs from written key")
	}
}

func TestLoadOnlyRefusesToCreate(t *testing.T) {
	path := keyPath(t)
	_, err := Load(path, false)
	if err == nil {
		t.Fatal("load-only missing file must error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err must wrap os.ErrNotExist: %v", err)
	}
	if !strings.Contains(err.Error(), "load-only") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("load-only call created the key file")
	}
}

func TestLoadOnlyHasNoSideEffects(t *testing.T) {
	// A refused load must not create the parent directory either.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sub", "identity.key")
	if _, err := Load(path, false); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(tmp, "sub")); !os.IsNotExist(err) {
		t.Fatal("load-only call created the parent directory")
	}
}

func TestCreateMakesParent0700(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sub", "identity.key")
	if _, err := Load(path, true); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(tmp, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&^0o700 != 0 {
		t.Fatalf("parent mode %#o exceeds 0700", st.Mode().Perm())
	}
}

func TestMkdirFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	// The mkdir error branch fires when the leaf is missing (ENOENT) but the
	// parent cannot be created — here, an unwritable ancestor.
	tmp := t.TempDir()
	ro := filepath.Join(tmp, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := Load(filepath.Join(ro, "sub", "identity.key"), true)
	if err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("err = %v", err)
	}
}

func TestParentIsAFile(t *testing.T) {
	// A file in the path components surfaces the raw ENOTDIR stat error and
	// never reaches the create arm (mkdir moved to the create branch, so a
	// refused load has no side effects).
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(filepath.Join(blocker, "identity.key"), true)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want a non-ENOENT stat failure", err)
	}
}

func TestWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "ro")
	if err := os.Mkdir(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := Load(filepath.Join(parent, "identity.key"), true)
	if err == nil || !strings.Contains(err.Error(), "identity:") {
		t.Fatalf("err = %v", err)
	}
}

// TestCreateUnderHostileUmask pins the post-create Chmod (decision S1-6): a
// umask that strips owner bits must not brick the next load.
func TestCreateUnderHostileUmask(t *testing.T) {
	path := keyPath(t) // create the temp dir before the umask bites
	old := syscall.Umask(0o277)
	defer syscall.Umask(old)
	if _, err := Load(path, true); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != KeyFilePerms {
		t.Fatalf("mode under umask 0277 = %#o, want %#o", st.Mode().Perm(), KeyFilePerms)
	}
	if _, err := Load(path, false); err != nil {
		t.Fatalf("immediate reload after hostile-umask create failed: %v", err)
	}
}

func TestSymlinkToValidKeyLoads(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real.key")
	if _, err := Load(target, true); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link.key")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// os.Stat follows links; the target's perms are what's checked.
	if _, err := Load(link, false); err != nil {
		t.Fatalf("symlink to valid key must load: %v", err)
	}
}
