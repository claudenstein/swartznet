// Package config holds the runtime configuration for a SwartzNet instance.
//
// Configuration is intentionally minimal for M1: the fields here are the ones
// the Engine needs to bring up an anacrolix/torrent Client. Later milestones
// will add index paths, publisher keys, search caps, etc. — each new field
// should have a sensible default so that an out-of-the-box run still works.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Config is the top-level runtime configuration for a SwartzNet instance.
//
// The zero value is not usable; call Default() and then override fields.
type Config struct {
	// DataDir is the filesystem directory where downloaded torrent content
	// is stored. It is created if missing.
	DataDir string

	// ListenPort is the TCP/uTP listen port for the BitTorrent peer wire. A
	// value of 0 lets the OS pick a free port (convenient for tests and
	// multi-instance local runs).
	ListenPort int

	// Seed, when true, means the Engine will continue seeding torrents after
	// download completes (the default behaviour of any well-behaved client).
	Seed bool

	// NoUpload disables all uploading (leech-only mode). Mutually exclusive
	// with Seed in spirit; if both are set, NoUpload wins.
	NoUpload bool

	// DisableDHT, when true, prevents the Engine from joining the mainline
	// DHT. Useful for isolated tests, harmful in normal use.
	DisableDHT bool

	// HTTPUserAgent overrides the HTTP user-agent string sent to trackers.
	// Leave empty to use anacrolix/torrent's default.
	HTTPUserAgent string

	// IndexDir is the filesystem directory where the local Bleve full-text
	// index is stored. It is created if missing. Default:
	// ~/.local/share/swartznet/index.
	IndexDir string
}

// Default returns a Config populated with sensible defaults for a normal
// desktop run. DataDir defaults to ~/.local/share/swartznet/data, following
// the XDG Base Directory spec where possible.
func Default() Config {
	return Config{
		DataDir:       defaultDataDir(),
		ListenPort:    42069, // same as anacrolix/torrent's default; reduces port surprise
		Seed:          true,
		NoUpload:      false,
		DisableDHT:    false,
		HTTPUserAgent: "", // use anacrolix default
		IndexDir:      defaultIndexDir(),
	}
}

// Validate checks invariants that cannot be enforced by the type system and
// creates the DataDir and IndexDir if they don't already exist. Returns a
// non-nil error if the Config cannot be used.
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("config: DataDir must not be empty")
	}
	if c.ListenPort < 0 || c.ListenPort > 65535 {
		return fmt.Errorf("config: ListenPort %d out of range", c.ListenPort)
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("config: cannot create DataDir %q: %w", c.DataDir, err)
	}
	if c.IndexDir != "" {
		// IndexDir's parent must exist; Bleve itself creates the leaf.
		if err := os.MkdirAll(filepath.Dir(c.IndexDir), 0o755); err != nil {
			return fmt.Errorf("config: cannot create parent of IndexDir %q: %w", c.IndexDir, err)
		}
	}
	return nil
}

// defaultDataDir returns the platform-appropriate default data directory.
// Order of preference:
//  1. $XDG_DATA_HOME/swartznet/data  (explicit XDG override)
//  2. $HOME/.local/share/swartznet/data  (Linux/XDG fallback)
//  3. ./swartznet-data  (last resort if HOME is unset)
func defaultDataDir() string {
	return filepath.Join(swartznetShareRoot(), "data")
}

// defaultIndexDir returns the platform-appropriate default Bleve index dir.
// It sits next to DataDir under the shared SwartzNet share root.
func defaultIndexDir() string {
	return filepath.Join(swartznetShareRoot(), "index")
}

// swartznetShareRoot returns the per-user root directory SwartzNet uses for
// all its persistent state (torrent data, index, later keys + reputation db).
func swartznetShareRoot() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "swartznet")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "swartznet")
	}
	return "./swartznet-state"
}
