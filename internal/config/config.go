// Package config holds the SwartzNet configuration struct, XDG path
// resolution, and validation. It imports no subsystem: no Bleve, no DHT, no
// wire, no HTTP.
//
// Path semantics: an empty path consistently means "feature off" (SPEC §5.8).
// Validate creates exactly two directories — DataDir and IndexDir's parent —
// and nothing else.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Config is the SwartzNet node configuration shared by every frontend.
// Fields arrive slice by slice as their subsystems land; only the walking
// skeleton's fields exist so far.
type Config struct {
	// DataDir receives downloaded content. Must be non-empty.
	DataDir string
	// IndexDir holds the Bleve search index. Empty disables indexing.
	// Validate creates only its parent: Bleve insists on creating the leaf.
	IndexDir string
	// IdentityPath locates the persistent ed25519 node identity (a raw
	// 64-byte key file, mode exactly 0600). Empty disables identity
	// entirely. Validate never touches it — the identity loader owns its
	// own parent-dir creation.
	IdentityPath string
	// ListenPort is the BitTorrent listen port (0 = OS-assigned).
	ListenPort int

	// Regtest and DHTInsecure are test-only knobs, refused outside test
	// binaries unless SWARTZNET_UNSAFE=1 (the single unsafe gate).
	Regtest     bool
	DHTInsecure bool
}

// Default returns the XDG-derived default configuration.
func Default() Config {
	root := ResolveShareRoot()
	return Config{
		DataDir:      filepath.Join(root, "data"),
		IndexDir:     filepath.Join(root, "index"),
		IdentityPath: filepath.Join(root, "identity.key"),
		ListenPort:   42069,
	}
}

// ResolveShareRoot resolves the SwartzNet share root:
// $XDG_DATA_HOME/swartznet, else $HOME/.local/share/swartznet, else the
// relative ./swartznet-state as a last resort when no home is known.
func ResolveShareRoot() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "swartznet")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "swartznet")
	}
	return "./swartznet-state"
}

// Validate checks the configuration and performs its only permitted side
// effects: creating DataDir (0755) and IndexDir's parent. All rejections run
// before either mkdir, so a rejected config leaves the filesystem untouched.
func (c Config) Validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("config: DataDir must not be empty")
	}
	if c.ListenPort < 0 || c.ListenPort > 65535 {
		return fmt.Errorf("config: ListenPort %d out of range", c.ListenPort)
	}
	if err := c.checkUnsafe(testing.Testing()); err != nil {
		return err
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("config: cannot create DataDir %q: %w", c.DataDir, err)
	}
	if c.IndexDir != "" {
		if err := os.MkdirAll(filepath.Dir(c.IndexDir), 0o755); err != nil {
			return fmt.Errorf("config: cannot create parent of IndexDir %q: %w", c.IndexDir, err)
		}
	}
	return nil
}

// checkUnsafe rejects the test-only knobs unless authorized. inTest is
// threaded as a parameter so tests can exercise the production branch.
func (c Config) checkUnsafe(inTest bool) error {
	if !c.Regtest && !c.DHTInsecure {
		return nil
	}
	if UnsafeAuthorized(inTest) {
		return nil
	}
	return fmt.Errorf("config: Regtest/DHTInsecure are test-only knobs; set SWARTZNET_UNSAFE=1 to use them outside tests")
}

// UnsafeAuthorized reports whether test-only behavior is authorized: inside
// any test binary, or when SWARTZNET_UNSAFE is exactly "1" ("yes"/"true" do
// not unlock). This is the single gate for every unsafe knob at every layer.
func UnsafeAuthorized(inTest bool) bool {
	return inTest || os.Getenv("SWARTZNET_UNSAFE") == "1"
}
