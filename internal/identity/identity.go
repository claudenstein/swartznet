package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// KeyFilePerms is the only file mode the loader will accept on the
// private-key file. 0600 means owner-read-write only; group and other
// have no access. Permissions of 0644 or laxer are rejected as
// "compromised", which forces the user to either fix them or delete
// the file (and let the loader regenerate it).
const KeyFilePerms fs.FileMode = 0o600

// Identity is a SwartzNet node's persistent ed25519 keypair plus the
// path it was loaded from. Construct via LoadOrCreate.
type Identity struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
	Path       string
}

// LoadOrCreate reads the identity at path, generating a fresh one if
// the file does not exist. Returns an error if the file exists but is
// unreadable, has wrong permissions, or contains a malformed key.
//
// The parent directory is created with mode 0700 if missing.
func LoadOrCreate(path string) (*Identity, error) {
	if path == "" {
		return nil, errors.New("identity: path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("identity: mkdir %q: %w", filepath.Dir(path), err)
	}

	if id, err := loadFromDisk(path); err == nil {
		return id, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// File missing → generate a new keypair and persist it.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate: %w", err)
	}
	if err := os.WriteFile(path, priv, KeyFilePerms); err != nil {
		return nil, fmt.Errorf("identity: write %q: %w", path, err)
	}
	return &Identity{PrivateKey: priv, PublicKey: pub, Path: path}, nil
}

// loadFromDisk reads and validates an existing identity file.
// Returns os.ErrNotExist if the file is absent so callers can branch
// cleanly between "load" and "create".
func loadFromDisk(path string) (*Identity, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return nil, fmt.Errorf("identity: %q is a directory, not a key file", path)
	}
	// Permission gate: only 0600 is acceptable. We compare the
	// permission bits explicitly so umask quirks during test setup
	// can't mask a mis-mode in real use.
	if mode := stat.Mode().Perm(); mode != KeyFilePerms {
		return nil, fmt.Errorf("identity: %q has insecure permissions %#o, want %#o (chmod or delete the file)",
			path, mode, KeyFilePerms)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("identity: read %q: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("identity: %q has size %d, want %d (corrupt key file)",
			path, len(raw), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(raw)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("identity: %q does not contain a valid ed25519 key", path)
	}
	return &Identity{PrivateKey: priv, PublicKey: pub, Path: path}, nil
}

// PublicKeyBytes returns the 32-byte ed25519 public key as a fixed
// array, which matches the type the BEP-44 layer wants.
func (id *Identity) PublicKeyBytes() [32]byte {
	var out [32]byte
	copy(out[:], id.PublicKey)
	return out
}

// PublicKeyHex returns the public key as a 64-char lowercase hex
// string. Useful for log output and CLI display.
func (id *Identity) PublicKeyHex() string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, len(id.PublicKey)*2)
	for i, b := range id.PublicKey {
		out[i*2] = hexDigits[b>>4]
		out[i*2+1] = hexDigits[b&0x0f]
	}
	return string(out)
}
