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

	// ListenHost, when non-empty, binds the BitTorrent peer-wire
	// and DHT listeners to the given interface (e.g. "127.0.0.1"
	// to stay on loopback). Empty (the default) delegates to
	// anacrolix/torrent's default, which is "" → 0.0.0.0.
	//
	// Operators rarely need to touch this; the in-process testlab
	// harness sets it to "127.0.0.1" so the DHT's BEP-44 tokens
	// (which are keyed on the query's source IP) stay consistent
	// across a multi-node cluster. Without it the OS may pick
	// different source IPs for sibling queries and token
	// validation silently rejects the put — which is how the
	// Layer-B s12 scenario's end-to-end put/get timed out.
	ListenHost string

	// Seed, when true, means the Engine will continue seeding torrents after
	// download completes (the default behaviour of any well-behaved client).
	Seed bool

	// NoUpload disables all uploading (leech-only mode). Mutually exclusive
	// with Seed in spirit; if both are set, NoUpload wins.
	NoUpload bool

	// DisableDHT, when true, prevents the Engine from joining the mainline
	// DHT. Useful for isolated tests, harmful in normal use.
	DisableDHT bool

	// DisableDHTPublish, when true, keeps the node joined to the
	// DHT (so it can do lookups and fetch BEP-46 companion-index
	// pointers) but skips every outbound BEP-44 mutable-item put.
	// This is the "leech-only DHT" mode recommended in
	// docs/08-operations.md for privacy-conscious operators: it
	// removes the hourly IP-exposure and timing-fingerprint
	// surface that comes with publishing, at the cost of losing
	// Layer-D contribution to the network. Default: false.
	DisableDHTPublish bool

	// DHTBootstrapAddrs, when non-empty, pre-seeds the anacrolix
	// DHT server's StartingNodes list with these host:port
	// addresses instead of the mainline defaults
	// (router.bittorrent.com etc.). Used by the Layer-B testbed
	// to form a private DHT on a docker bridge where the default
	// bootstrap hosts are unreachable. Empty slice means "use
	// anacrolix's default public bootstrap nodes", which is
	// correct for any real deployment. Each entry is a single
	// host:port string; the engine resolves them at DHT-server
	// configuration time. Default: nil.
	DHTBootstrapAddrs []string

	// DisableIPv6, when true, prevents the embedded torrent client
	// from opening udp6 / tcp6 listeners. The client otherwise
	// spins up both v4 and v6 sockets; for the embedded DHT
	// that becomes two DHT servers per node, and the Engine's
	// Publisher only drives one of them. Cross-node traversals
	// end up with IPv4-mapped-IPv6 addresses like
	// [::ffff:127.0.0.1]:X in their routing tables, which puts
	// cannot round-trip. Flipping this keeps the harness on a
	// single address family end-to-end. Also useful on networks
	// that have no functional IPv6 path (many corporate LANs,
	// most docker bridges). Default: false.
	DisableIPv6 bool

	// DHTInsecure, when true, disables BEP-42 node-ID security
	// enforcement on the anacrolix DHT server (maps to
	// dht.ServerConfig.NoSecurity). BEP-42 ties a node's 20-byte
	// ID to its public IP so a single host can't cheaply forge
	// many identities (Sybil resistance on mainline). In a
	// private testbed DHT that lives entirely on a docker bridge
	// or k8s cluster, container IPs (172.16.0.0/12 style) never
	// produce a "secure" ID under BEP-42's rules, so anacrolix
	// silently drops every peer as "not secure" and traversals
	// return empty — BEP-44 put/get then times out. This flag
	// opts out so private networks can form a DHT at all. Must
	// be left at false on mainline: flipping it in production
	// is a Sybil-resistance regression. Default: false.
	DHTInsecure bool

	// Regtest, when true, activates "regtest mode" — a
	// deterministic fast-forward mode modeled on Bitcoin Core's
	// `-regtest`. Every production time constant is accelerated
	// so scenario tests that depend on "what happens after the
	// publisher refreshes" run in seconds instead of hours:
	//
	//   - dhtindex.Publisher.RefreshInterval:  1h → 5s
	//   - dhtindex.Publisher.MinPutInterval:  55m → 100ms
	//   - companion.Publisher.Interval:        1h → 10s
	//   - companion.Publisher.MinInterval:     1m → 100ms
	//
	// Regtest mode runs the exact same code paths as production
	// with one config flag flipped — no mocks, no stubs. It is
	// intended for:
	//   - the internal/testlab harness (which enables it on every
	//     spawned engine)
	//   - docker-compose / k8s testbed containers (Layer B/C)
	//   - CI jobs that run end-to-end scenarios
	//   - developers reproducing production bugs locally
	//
	// It must NEVER be used in production — a real node running
	// regtest mode would hammer the mainline DHT with a put every
	// 5 seconds and get ratelimited into the ground. The engine
	// logs engine.regtest_mode_active at Warn level on startup so
	// it's unmissable.
	//
	// Default: false.
	Regtest bool

	// HTTPUserAgent overrides the HTTP user-agent string sent to trackers.
	// Leave empty to use anacrolix/torrent's default.
	HTTPUserAgent string

	// IndexDir is the filesystem directory where the local Bleve full-text
	// index is stored. It is created if missing. Default:
	// ~/.local/share/swartznet/index.
	IndexDir string

	// IdentityPath is where the persistent ed25519 keypair lives.
	// Default: ~/.local/share/swartznet/identity.key. The key is
	// generated on first run and reused thereafter; it identifies
	// this node as a publisher of BEP-44 keyword-index entries
	// (Layer D, M4).
	IdentityPath string

	// PublisherManifest is the on-disk path to the per-keyword
	// manifest the dhtindex publisher writes. Default:
	// ~/.local/share/swartznet/publisher.json.
	PublisherManifest string

	// ReputationPath is the on-disk path to the per-pubkey
	// reputation tracker. Default:
	// ~/.local/share/swartznet/reputation.json.
	ReputationPath string

	// SeedListPath is the on-disk path to the curated indexer
	// seed list (M13c). The file is a JSON document of the form
	// {"version":1,"seeds":[{"pubkey":"<hex>","label":"<name>"}]}.
	// Every entry is imported via reputation.Tracker.MarkSeeded on
	// startup, which applies a decaying +0.45 score bonus with a
	// 90-day half-life. Missing file is not an error (the node
	// runs with a cold-start reputation network in that case).
	// Default: ~/.local/share/swartznet/seeds.json.
	SeedListPath string

	// BloomPath is the on-disk path to the known-good infohash
	// Bloom filter. Default:
	// ~/.local/share/swartznet/known-good.bloom.
	BloomPath string

	// MinIndexerScore is the reputation cutoff for Layer-D
	// lookup. Indexers below this score are skipped. 0 disables
	// the cutoff. Default: 0.
	MinIndexerScore float64

	// CompanionDir is the on-disk directory where the F3 companion
	// publisher (M11c) stores the gzipped JSON content index and
	// the wrapping .torrent file. Default:
	// ~/.local/share/swartznet/companion. Empty disables the
	// companion publisher entirely (the node still works for local
	// search and Layer-D queries; it just does not advertise its
	// content via a companion-index torrent).
	CompanionDir string

	// CompanionFollowFile is the on-disk JSON file that lists
	// publishers the M11d subscriber should follow. The file
	// holds a single JSON array of objects of the form
	// {"pubkey":"<64-char hex>","label":"<name>"}. Default:
	// ~/.local/share/swartznet/companion-follows.json. The file
	// is created on demand by the GUI; if it does not exist on
	// startup the subscriber starts with an empty follow list.
	CompanionFollowFile string

	// TrustPath is the on-disk JSON path for the publisher
	// trust list — ed25519 pubkeys whose signed .torrent files
	// get implicit trust (auto-confirmed to the known-good
	// Bloom filter, surfaced in search with a "trusted" flag).
	// Default: ~/.local/share/swartznet/trust.json. Empty
	// disables the trust list entirely (equivalent to trusting
	// nobody automatically).
	TrustPath string
}

// Default returns a Config populated with sensible defaults for a normal
// desktop run. DataDir defaults to ~/.local/share/swartznet/data, following
// the XDG Base Directory spec where possible.
func Default() Config {
	return Config{
		DataDir:             defaultDataDir(),
		ListenPort:          42069, // same as anacrolix/torrent's default; reduces port surprise
		Seed:                true,
		NoUpload:            false,
		DisableDHT:          false,
		HTTPUserAgent:       "", // use anacrolix default
		IndexDir:            defaultIndexDir(),
		IdentityPath:        defaultIdentityPath(),
		PublisherManifest:   defaultPublisherManifest(),
		ReputationPath:      defaultReputationPath(),
		SeedListPath:        defaultSeedListPath(),
		BloomPath:           defaultBloomPath(),
		MinIndexerScore:     0,
		CompanionDir:        defaultCompanionDir(),
		CompanionFollowFile: defaultCompanionFollowFile(),
		TrustPath:           defaultTrustPath(),
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

// defaultIdentityPath returns the platform-appropriate default path
// for the persistent ed25519 publisher key.
func defaultIdentityPath() string {
	return filepath.Join(swartznetShareRoot(), "identity.key")
}

// defaultPublisherManifest returns the platform-appropriate default
// path for the dhtindex publisher's per-keyword manifest.
func defaultPublisherManifest() string {
	return filepath.Join(swartznetShareRoot(), "publisher.json")
}

// defaultReputationPath returns the platform-appropriate default
// path for the per-pubkey reputation tracker.
func defaultReputationPath() string {
	return filepath.Join(swartznetShareRoot(), "reputation.json")
}

// defaultSeedListPath returns the platform-appropriate default
// path for the M13c curated indexer seed list.
func defaultSeedListPath() string {
	return filepath.Join(swartznetShareRoot(), "seeds.json")
}

// defaultBloomPath returns the platform-appropriate default path
// for the known-good infohash Bloom filter.
func defaultBloomPath() string {
	return filepath.Join(swartznetShareRoot(), "known-good.bloom")
}

// defaultCompanionDir returns the platform-appropriate default
// directory for the F3 companion publisher's on-disk artefacts.
func defaultCompanionDir() string {
	return filepath.Join(swartznetShareRoot(), "companion")
}

// defaultCompanionFollowFile returns the platform-appropriate
// default path for the F3 companion subscriber's follow list.
func defaultCompanionFollowFile() string {
	return filepath.Join(swartznetShareRoot(), "companion-follows.json")
}

// defaultTrustPath returns the platform-appropriate default
// path for the publisher trust list.
func defaultTrustPath() string {
	return filepath.Join(swartznetShareRoot(), "trust.json")
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
