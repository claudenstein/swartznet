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
	"time"
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
	// TrustPath locates the publisher allowlist (trust.json). Empty =
	// trust nobody.
	TrustPath string
	// BloomPath locates the known-good Bloom filter. Empty disables it.
	BloomPath string
	// ReputationPath locates the per-indexer reputation tracker. Empty
	// disables it.
	ReputationPath string
	// PublisherPath locates the Layer-D publisher manifest (publisher.json),
	// the full hit list this node re-announces on every refresh. Empty keeps
	// the manifest in memory only (a fresh process re-publishes from scratch).
	PublisherPath string
	// CompanionDir holds the companion content-index payload (*.json.gz) and
	// its wrapping .torrent (Slice 10). Empty disables BOTH the companion
	// publisher and subscriber.
	CompanionDir string
	// CompanionFollowFile is the JSON array of followed publisher pubkeys the
	// subscriber loads at startup and persists on follow/unfollow. Empty keeps
	// follows in memory only (added at runtime, lost on restart).
	CompanionFollowFile string
	// SeedListPath locates the reputation seed list (seeds.json). Empty
	// skips it.
	SeedListPath string
	// ListenPort is the BitTorrent listen port (0 = OS-assigned).
	ListenPort int
	// ListenHost, when non-empty, pins the BitTorrent/DHT bind host. An
	// isolated regtest DHT cluster needs 127.0.0.1 here — a 0.0.0.0 bind
	// lets the kernel flap the source IP per send, silently breaking the
	// BEP-44 write-token check.
	ListenHost string

	// Seed keeps completed torrents uploading (default true).
	Seed bool
	// NoUpload disables uploading entirely (leech-only debugging).
	NoUpload bool
	// DisableDHT turns the mainline DHT off.
	DisableDHT bool
	// DisableDHTPublish stays on the DHT but suppresses BEP-44 publication
	// (privacy knob; consumed by the Layer-D slice).
	DisableDHTPublish bool
	// NoIndex prevents the Bleve index from opening at all (consumed by the
	// indexer slice; the daemon mirrors its Options.NoIndex here before
	// engine construction).
	NoIndex bool
	// DHTBootstrapAddrs overrides the DHT bootstrap nodes (host:port).
	// Empty falls through to anacrolix's public routers — an isolated
	// cluster must seed a placeholder instead.
	DHTBootstrapAddrs []string
	// DisableIPv6 restricts networking to IPv4. Dual-stack spawns two DHT
	// servers but the publisher drives only one.
	DisableIPv6 bool
	// DisablePortForwarding turns off UPnP/NAT-PMP gateway calls. Hermetic
	// tests need it; operators behind hostile gateways may want it.
	DisablePortForwarding bool
	// HTTPUserAgent overrides the tracker/webseed user agent when non-empty.
	HTTPUserAgent string

	// IndexRescanInterval overrides the Layer-L rescan cadence (0 = the
	// default hour). Tests shrink it; operators need not set it.
	IndexRescanInterval time.Duration
	// CheckpointInterval overrides the Bloom+reputation checkpoint cadence
	// (0 = the default 5 minutes). Tests shrink it so the crash-safety
	// window is observable in seconds.
	CheckpointInterval time.Duration

	// Sharing prefs — the operator-controlled half of the sn_search
	// capability mask (Slice 6). Runtime-mutable via PATCH /capabilities;
	// these are the STARTUP defaults. ShareLocal is a tri-state (0 = don't
	// answer, 1 = in-swarm only, 2 = full local index).
	ShareLocal       int  // 0..2, default 2
	ShareFileHits    bool // default true
	ShareContentHits bool // default true

	// LayerDMode selects the Layer-D RecordBackend: "legacy" (the shipping
	// per-keyword BEP-44 index, the default), "aggregatePPMI" (the signed SNAGG
	// B-tree), or "composite" (dual-write legacy+aggregate, legacy-then-aggregate
	// dual-read — the zero-data-loss migration mode). Empty is "legacy".
	LayerDMode string
	// MinIndexerScore is the minimum reputation an indexer must have before
	// Layer-D lookups query it (SPEC §5.7). Zero (default) disables the gate;
	// it has no effect without a reputation tracker.
	MinIndexerScore float64

	// Rendezvous: capable-peer discovery on the mainline DHT. Nodes join
	// well-known "rendezvous" infohashes (ordinary BEP-5 swarms) so sn_search
	// peers find each other even with no content torrent in common. See
	// internal/rendezvous and docs/12.
	//
	// RendezvousGlobal joins the single public global swarm (default on). It makes
	// this node's SwartzNet membership publicly enumerable (anyone can get_peers
	// the well-known infohash) — a sharper exposure than plain swarm
	// participation; opt out with --no-rendezvous in a sensitive deployment.
	RendezvousGlobal bool
	// RendezvousTopics joins one swarm per topic, so nodes interested in the same
	// content meet (and there is no single global membership list). Explicit
	// opt-in; never auto-derived from the local index (which would leak it).
	RendezvousTopics []string
	// RendezvousCommunities joins a private swarm per shared secret
	// (HMAC-SHA1-derived), so a closed group meets without public enumeration.
	// SENSITIVE: these are secrets — never log or echo them.
	RendezvousCommunities []string

	// Regtest and DHTInsecure are test-only knobs, refused outside test
	// binaries unless SWARTZNET_UNSAFE=1 (the single unsafe gate).
	Regtest     bool
	DHTInsecure bool
}

// Default returns the XDG-derived default configuration.
func Default() Config {
	root := ResolveShareRoot()
	return Config{
		DataDir:        filepath.Join(root, "data"),
		IndexDir:       filepath.Join(root, "index"),
		IdentityPath:   filepath.Join(root, "identity.key"),
		TrustPath:      filepath.Join(root, "trust.json"),
		BloomPath:      filepath.Join(root, "known-good.bloom"),
		ReputationPath: filepath.Join(root, "reputation.json"),
		PublisherPath:  filepath.Join(root, "publisher.json"),
		SeedListPath:   filepath.Join(root, "seeds.json"),

		CompanionDir:        filepath.Join(root, "companion"),
		CompanionFollowFile: filepath.Join(root, "companion-follows.json"),
		ListenPort:          42069,
		Seed:                true,
		// Default sharing: full local index, file + content hits on.
		ShareLocal:       2,
		ShareFileHits:    true,
		ShareContentHits: true,
		// Layer D: the shipping per-keyword BEP-44 backend.
		LayerDMode: "legacy",
		// Discovery: join the public global rendezvous swarm by default so
		// capable peers find each other out of the box (opt out: --no-rendezvous).
		RendezvousGlobal: true,
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
	if c.ShareLocal < 0 || c.ShareLocal > 2 {
		return fmt.Errorf("config: ShareLocal %d out of range (0..2)", c.ShareLocal)
	}
	switch c.LayerDMode {
	case "", "legacy", "composite", "aggregatePPMI":
	default:
		return fmt.Errorf("config: LayerDMode %q unsupported (legacy|composite|aggregatePPMI)", c.LayerDMode)
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
