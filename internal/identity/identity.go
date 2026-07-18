// Package identity owns the persistent ed25519 node identity (invariant #2).
// The identity backs publisher reputation and torrent signing; losing it
// loses standing, so a present-but-invalid key file is never overwritten or
// regenerated — only a missing file at the default path is ever created.
//
// On-disk format (frozen, SPEC §3): the raw 64-byte ed25519 private key —
// seed at [0:32], public key at [32:64] — no encoding, mode exactly 0600.
//
// This package imports only the standard library: it knows nothing of
// signing formats, the DHT, the wire, or configuration.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// KeyFilePerms is the exact mode identity.key is created with and must keep.
// The load gate compares literal permission bits, so umask quirks cannot
// mask a mis-mode; stricter modes (0400) are rejected too.
const KeyFilePerms fs.FileMode = 0o600

// Identity is a loaded node identity.
type Identity struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
	// Path is where the key was loaded from or created.
	Path string
}

// Load loads the identity at path. When the file is missing, a new keypair
// is minted and persisted only if allowCreate is true — callers pass true
// only when path is the default XDG identity path, which makes this create
// branch the single enforcement site of "auto-create only at the default
// path". Every other load failure propagates unchanged: a bad key file is
// never regenerated.
func Load(path string, allowCreate bool) (*Identity, error) {
	if path == "" {
		return nil, fmt.Errorf("identity: path must not be empty")
	}
	id, err := loadFromDisk(path)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !allowCreate {
		return nil, fmt.Errorf("identity: %q does not exist and this path is load-only (only the default identity path is auto-created): %w", path, err)
	}
	return create(path)
}

func loadFromDisk(path string) (*Identity, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("identity: %q is a directory, not a key file", path)
	}
	if perm := st.Mode().Perm(); perm != KeyFilePerms {
		return nil, fmt.Errorf("identity: %q has insecure permissions %#o, want %#o (chmod or delete the file)", path, perm, KeyFilePerms)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("identity: read %q: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("identity: %q has size %d, want %d (corrupt key file)", path, len(raw), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(raw)
	derived := ed25519.NewKeyFromSeed(priv.Seed())
	if !derived.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(raw[ed25519.SeedSize:])) {
		return nil, fmt.Errorf("identity: %q public half does not match seed (corrupt key file)", path)
	}
	return &Identity{
		PrivateKey: priv,
		PublicKey:  priv.Public().(ed25519.PublicKey),
		Path:       path,
	}, nil
}

// create mints and persists a fresh keypair. The parent directory is created
// here — not on the load path — so a refused load has no side effects.
func create(path string) (*Identity, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("identity: mkdir %q: %w", filepath.Dir(path), err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate: %w", err)
	}
	if err := os.WriteFile(path, priv, KeyFilePerms); err != nil {
		return nil, fmt.Errorf("identity: write %q: %w", path, err)
	}
	// WriteFile's mode is umask-subject; the load gate demands exactly 0600.
	if err := os.Chmod(path, KeyFilePerms); err != nil {
		return nil, fmt.Errorf("identity: chmod %q: %w", path, err)
	}
	return &Identity{
		PrivateKey: priv,
		PublicKey:  priv.Public().(ed25519.PublicKey),
		Path:       path,
	}, nil
}

// PublicKeyBytes returns the public key as a fixed 32-byte array (the shape
// the BEP-44 layer keys on).
func (id *Identity) PublicKeyBytes() [32]byte {
	var b [32]byte
	copy(b[:], id.PublicKey)
	return b
}

// PublicKeyHex returns the public key as 64 lowercase hex characters.
func (id *Identity) PublicKeyHex() string {
	return hex.EncodeToString(id.PublicKey)
}

// Signer signs messages with the node identity. Every publisher-shaped
// consumer (Layer-D publishing, torrent signing, record minting) receives
// this same signer from the daemon.
type Signer struct {
	priv ed25519.PrivateKey
}

// Signer returns a Signer over the identity's private key.
func (id *Identity) Signer() Signer {
	return Signer{priv: id.PrivateKey}
}

// Sign returns the ed25519 signature of message (pure ed25519, no
// pre-hashing).
func (s Signer) Sign(message []byte) []byte {
	return ed25519.Sign(s.priv, message)
}

// Public returns the signer's public key.
func (s Signer) Public() ed25519.PublicKey {
	return s.priv.Public().(ed25519.PublicKey)
}
