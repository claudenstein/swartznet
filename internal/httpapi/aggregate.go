// GET /aggregate — Aggregate subsystem introspection endpoint.
//
// Returns a JSON summary of the v0.5 Aggregate track state:
//
//   - whether the PPMI path is enabled (lookup has a PPMIGetter)
//   - known indexer pubkeys (from Lookup.Indexers())
//   - RecordSource kind + cache size when attached
//   - sn_search BitSetReconciliation capability advertised
//
// Intended as a lightweight operator probe; pairs with `swartznet
// aggregate inspect`/`find` for file-level work. Empty when the
// daemon has no Aggregate subsystem wired, which is the default
// until engine integration lands.

package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// AggregateStatusResponse is the GET /aggregate payload shape.
type AggregateStatusResponse struct {
	// PPMIEnabled is true when Lookup has a PPMIGetter attached.
	// Clients treat false as "this daemon still reads only
	// legacy per-keyword items" per the v0.5 dual-read migration.
	PPMIEnabled bool `json:"ppmi_enabled"`

	// KnownIndexers is the count of publisher pubkeys in the
	// Lookup fan-out set. Populated from any channel A/B/C hits
	// plus hardcoded anchors.
	KnownIndexers int `json:"known_indexers"`

	// Indexers is the detailed list — each entry carries hex
	// pubkey + optional human label. Order is snapshot-stable
	// but not sorted; UI should sort if display order matters.
	Indexers []AggregateIndexer `json:"indexers,omitempty"`

	// RecordSourceKind identifies the RecordSource type (if any).
	// "cache" for RecordCache; "custom" for other impls;
	// "" when no source is attached.
	RecordSourceKind string `json:"record_source_kind,omitempty"`

	// RecordCacheSize is the number of LocalRecords held in the
	// RecordSource when it is a *RecordCache. Zero otherwise.
	RecordCacheSize int `json:"record_cache_size,omitempty"`

	// ServicesAdvertised is the hex-encoded ServiceBits this
	// daemon puts on its peer_announce frames. Clients check bit
	// 9 (BitSetReconciliation = 0x200) to confirm the sync
	// protocol is enabled locally.
	ServicesAdvertised string `json:"services,omitempty"`

	// Bootstrap reports the cold-start orchestrator's state —
	// number of anchor pubkeys, admitted publishers, pending
	// candidates awaiting admission. Nil when no Bootstrap is
	// attached (DHT-off daemon, or legacy daemon lacking the
	// probe hook).
	Bootstrap *AggregateBootstrap `json:"bootstrap,omitempty"`
}

// AggregateBootstrap is the introspection payload for the
// three-channel cold-start orchestrator (SPEC §3).
type AggregateBootstrap struct {
	// Anchors is the count of hardcoded+HTTPS-fetched anchor
	// pubkeys currently in the bootstrap set.
	Anchors int `json:"anchors"`
	// Admitted is the count of publishers admitted via any
	// channel (A/B/C).
	Admitted int `json:"admitted"`
}

// BootstrapProbe is the minimal interface the httpapi layer
// needs to read Bootstrap state without taking a direct import
// on internal/daemon (which would create an import cycle).
// daemon.Bootstrap satisfies it natively.
type BootstrapProbe interface {
	AnchorCount() int
	AdmittedCount() int
}

// AggregateIndexer is one entry in the Indexers array.
type AggregateIndexer struct {
	PubKey string `json:"pk"` // 64-char hex
	Label  string `json:"label,omitempty"`
}

// handleAggregateStatus serves GET /aggregate.
func (s *Server) handleAggregateStatus(w http.ResponseWriter, r *http.Request) {
	var resp AggregateStatusResponse

	if s.lookup != nil {
		resp.PPMIEnabled = s.lookup.PPMIGetter() != nil
		for _, info := range s.lookup.Indexers() {
			resp.Indexers = append(resp.Indexers, AggregateIndexer{
				PubKey: hex.EncodeToString(info.PubKey[:]),
				Label:  info.Label,
			})
		}
		resp.KnownIndexers = len(resp.Indexers)
	}

	if s.swarm != nil {
		if src := s.swarm.RecordSource(); src != nil {
			// Identify the source by type: the common in-repo
			// implementation is *RecordCache; anything else is
			// reported as "custom" without leaking internals.
			if cache, ok := src.(*swarmsearch.RecordCache); ok {
				resp.RecordSourceKind = "cache"
				resp.RecordCacheSize = cache.Len()
			} else {
				resp.RecordSourceKind = "custom"
			}
		}

		services := swarmsearch.DefaultServices()
		resp.ServicesAdvertised = formatServicesHex(uint64(services))
	}

	if s.bootstrap != nil {
		resp.Bootstrap = &AggregateBootstrap{
			Anchors:  s.bootstrap.AnchorCount(),
			Admitted: s.bootstrap.AdmittedCount(),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// formatServicesHex renders a ServiceBits value as lowercase hex
// without a leading "0x" — consistent with how other hex fields
// (pubkeys, fingerprints) render across the codebase.
func formatServicesHex(v uint64) string {
	b := make([]byte, 8)
	for i := 0; i < 8; i++ {
		b[7-i] = byte(v >> (i * 8))
	}
	return hex.EncodeToString(b)
}
