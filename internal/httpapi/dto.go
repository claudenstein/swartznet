package httpapi

// The /status JSON shape is frozen here in full, even though most blocks stay
// empty until their subsystems land: the field names are a compatibility
// contract with the CLI, web UI, and scripts. Every DTO is declared locally —
// httpapi imports no subsystem, and the daemon adapts subsystem types into
// these field by field.

// StatusResponse is the GET /status document. local/swarm/publisher are
// always present (zero-valued when unwired); dht/bloom/reputation are
// omitted entirely when their subsystem is absent — for the DHT block,
// "omitted" means disabled, which is deliberately distinct from a present
// block with zero nodes (an isolated-but-enabled node).
type StatusResponse struct {
	Local      LocalStatus     `json:"local"`
	Swarm      SwarmStatus     `json:"swarm"`
	Publisher  PublisherStatus `json:"publisher"`
	DHT        *DHTStatus      `json:"dht,omitempty"`
	Bloom      *BloomStatus    `json:"bloom,omitempty"`
	Reputation *ReputationStat `json:"reputation,omitempty"`
}

// LocalStatus reports Layer L (the local Bleve index).
type LocalStatus struct {
	Indexed  bool   `json:"indexed"` // true once the index collaborator is wired
	DocCount uint64 `json:"doc_count"`
}

// SwarmStatus reports Layer S (sn_search peer-wire).
type SwarmStatus struct {
	KnownPeers   int `json:"known_peers"`
	CapablePeers int `json:"capable_peers"`
}

// PublisherStatus reports Layer D publishing.
type PublisherStatus struct {
	PubKey        string                  `json:"pubkey,omitempty"`
	TotalKeywords int                     `json:"total_keywords"`
	TotalHits     int                     `json:"total_hits"`
	Keywords      []PublisherKeywordEntry `json:"keywords,omitempty"`
}

// PublisherKeywordEntry is one published keyword's state.
type PublisherKeywordEntry struct {
	Keyword       string `json:"keyword"`
	HitsCount     int    `json:"hits_count"`
	LastPublished string `json:"last_published,omitempty"` // RFC3339 UTC; omitted at zero time
	PublishCount  int    `json:"publish_count"`
	LastError     string `json:"last_error,omitempty"`
}

// DHTStatus reports the DHT routing table when the DHT is enabled.
type DHTStatus struct {
	GoodNodes int `json:"good_nodes"`
	Nodes     int `json:"nodes"`
}

// BloomStatus reports the known-good Bloom filter.
type BloomStatus struct {
	BitSize        uint64  `json:"bit_size"`
	HashFunctions  uint64  `json:"hash_functions"`
	PopulationBits uint64  `json:"population_bits"`
	EstimatedItems float64 `json:"estimated_items"`
}

// ReputationStat reports the per-indexer reputation tracker.
type ReputationStat struct {
	KnownIndexers int                        `json:"known_indexers"`
	TopIndexers   []ReputationIndexerSummary `json:"top_indexers,omitempty"` // truncated to 10
}

// ReputationIndexerSummary is one indexer's reputation summary.
type ReputationIndexerSummary struct {
	PubKey        string  `json:"pubkey"`
	Score         float64 `json:"score"`
	HitsReturned  int     `json:"hits_returned"`
	HitsConfirmed int     `json:"hits_confirmed"`
	HitsFlagged   int     `json:"hits_flagged"`
}

// healthzResponse is the GET /healthz document.
type healthzResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
}
