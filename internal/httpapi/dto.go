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

// TorrentSnapshot is one torrent's state in GET /torrents. The status
// vocabulary is a cross-layer enum: metadata|downloading|seeding|paused|
// queued — exactly five values; "complete" is never emitted.
type TorrentSnapshot struct {
	InfoHash       string  `json:"infohash"`
	Name           string  `json:"name"`
	Size           int64   `json:"size"`
	BytesCompleted int64   `json:"bytes_completed"`
	BytesMissing   int64   `json:"bytes_missing"`
	Progress       float64 `json:"progress"`
	Files          int     `json:"files"`
	ActivePeers    int     `json:"active_peers"`
	HalfOpenPeers  int     `json:"half_open_peers"`
	PendingPeers   int     `json:"pending_peers"`
	TotalPeers     int     `json:"total_peers"`
	Seeders        int     `json:"seeders"`
	Paused         bool    `json:"paused"`
	Status         string  `json:"status"`
	Indexing       bool    `json:"indexing"`
	IndexedFiles   int     `json:"indexed_files,omitempty"`
	IndexExtracted int     `json:"index_extracted,omitempty"`
	Queued         bool    `json:"queued"`
	DownloadRate   int64   `json:"download_rate"`
	UploadRate     int64   `json:"upload_rate"`
	SignedBy       string  `json:"signed_by,omitempty"`
	TrustedPub     bool    `json:"trusted_publisher,omitempty"`
}

// TorrentFile is one file in GET /torrents/{ih}/files.
type TorrentFile struct {
	Index          int     `json:"index"`
	Path           string  `json:"path"`
	DisplayPath    string  `json:"display_path"`
	Length         int64   `json:"length"`
	BytesCompleted int64   `json:"bytes_completed"`
	Progress       float64 `json:"progress"`
	Priority       string  `json:"priority"` // none | normal | high
}

// TorrentsResponse is the GET /torrents document.
type TorrentsResponse struct {
	Torrents []TorrentSnapshot `json:"torrents"` // [] never null
}

// FilesListResponse is the GET /torrents/{ih}/files document. The CLI's
// files command decodes it.
type FilesListResponse struct {
	InfoHash string        `json:"infohash"`
	Files    []TorrentFile `json:"files"`
}

// AddTorrentRequest is the POST /torrent body (magnet URIs only over HTTP).
type AddTorrentRequest struct {
	URI string `json:"uri"`
}

// AddTorrentResponse acknowledges an add; metadata fetch is async.
type AddTorrentResponse struct {
	OK       bool   `json:"ok"`
	InfoHash string `json:"infohash"`
}

// RateLimitRequest is the PATCH/POST /config/rate-limit body. Pointer
// fields carry merge semantics: absent = unchanged, present ≤0 = unlimited —
// the legacy zeroed whatever was omitted (§6).
type RateLimitRequest struct {
	UploadBps   *int64 `json:"upload_bps"`
	DownloadBps *int64 `json:"download_bps"`
}

// RateLimitResponse is the GET /config/rate-limit document (0 = unlimited).
type RateLimitResponse struct {
	UploadBps   int64 `json:"upload_bps"`
	DownloadBps int64 `json:"download_bps"`
}

// QueueConfigRequest is the PATCH/POST /config/queue body (merge semantics).
type QueueConfigRequest struct {
	MaxActiveDownloads *int `json:"max_active_downloads"`
}

// QueueConfigResponse is the GET /config/queue document (0 = unlimited).
type QueueConfigResponse struct {
	MaxActiveDownloads int `json:"max_active_downloads"`
}

// SearchRequestBody is the POST /search body. Swarm/DHT fields are frozen
// now though ignored until their layers land.
type SearchRequestBody struct {
	Q            string `json:"q"`
	Limit        int    `json:"limit,omitempty"`
	Swarm        bool   `json:"swarm,omitempty"`
	DHT          bool   `json:"dht,omitempty"`
	SwarmTimeout int    `json:"swarm_timeout_ms,omitempty"`
	DHTTimeout   int    `json:"dht_timeout_ms,omitempty"`
	SignedBy     string `json:"signed_by,omitempty"`
	Highlight    bool   `json:"highlight,omitempty"`
}

// SearchParams is what the daemon adapter receives (httpapi owns it, so the
// package imports no searchmux/indexer types).
type SearchParams struct {
	Query          string
	Limit          int
	SignedBy       string
	Highlight      bool
	Swarm          bool
	SwarmTimeoutMS int
	DHT            bool
	DHTTimeoutMS   int
}

// LocalHit is one Layer-L result. file_index is omitempty so a content hit
// in file 0 omits it (frozen legacy quirk).
type LocalHit struct {
	DocType   string              `json:"doc_type"`
	InfoHash  string              `json:"infohash"`
	Name      string              `json:"name,omitempty"`
	SizeBytes int64               `json:"size_bytes,omitempty"`
	FileIndex int                 `json:"file_index,omitempty"`
	FilePath  string              `json:"file_path,omitempty"`
	Mime      string              `json:"mime,omitempty"`
	Extractor string              `json:"extractor,omitempty"`
	Score     float64             `json:"score"`
	SignedBy  string              `json:"signed_by,omitempty"`
	Fragments map[string][]string `json:"fragments,omitempty"`
}

// LocalBlock is the Layer-L portion of a search response.
type LocalBlock struct {
	Total uint64     `json:"total"`
	Hits  []LocalHit `json:"hits"` // never null
}

// SearchResult is the adapter's return: the local block plus an error the
// handler maps to 500 (Layer-L failure is fatal to the whole request), and the
// optional swarm/dht blocks (a Layer-S or Layer-D failure is surfaced INLINE,
// never a 500).
type SearchResult struct {
	Local    LocalBlock
	LocalErr error
	Swarm    *SwarmBlock
	Dht      *DHTBlock
}

// SearchResponse is the POST /search document. The swarm/dht blocks appear only
// when the request asked for them AND the collaborator is wired.
type SearchResponse struct {
	Local LocalBlock  `json:"local"`
	Swarm *SwarmBlock `json:"swarm,omitempty"`
	Dht   *DHTBlock   `json:"dht,omitempty"`
}

// DHTBlock is the Layer-D portion of a search response. A Layer-D failure
// renders as the Error string with a 200 (§5.9), never a 5xx.
type DHTBlock struct {
	IndexersAsked     int      `json:"indexers_asked"`
	IndexersResponded int      `json:"indexers_responded"`
	Hits              []DHTHit `json:"hits"` // never null
	Error             string   `json:"error,omitempty"`
}

// DHTHit is one merged Layer-D result.
type DHTHit struct {
	InfoHash string   `json:"infohash"`
	Name     string   `json:"name"`
	Size     int64    `json:"size,omitempty"`
	Seeders  int      `json:"seeders,omitempty"`
	Score    float64  `json:"score"`
	BloomHit bool     `json:"bloom_hit,omitempty"`
	Sources  []string `json:"sources"`
}

// SwarmBlock is the Layer-S portion of a search response. A Layer-S failure
// renders as the Error string with a 200 (§5.9), never a 5xx.
type SwarmBlock struct {
	Asked     int        `json:"asked"`
	Responded int        `json:"responded"`
	Rejected  int        `json:"rejected"`
	Hits      []SwarmHit `json:"hits"` // never null
	Error     string     `json:"error,omitempty"`
}

// SwarmHit is one merged Layer-S result.
type SwarmHit struct {
	InfoHash string   `json:"infohash"`
	Name     string   `json:"name"`
	Size     int64    `json:"size,omitempty"`
	Seeders  int      `json:"seeders,omitempty"`
	Score    int      `json:"score"`
	Sources  []string `json:"sources"`
}

// ConfirmRequest / FlagRequest carry a single 40-hex infohash.
type FlagRequest struct {
	InfoHash string `json:"infohash"`
}

// ConfirmResult is what the daemon's shared Confirm path returns.
type ConfirmResult struct {
	InfoHash          string
	IndexersConfirmed int
}

// ConfirmResponse is the POST /confirm document.
type ConfirmResponse struct {
	OK                bool   `json:"ok"`
	InfoHash          string `json:"infohash"`
	IndexersConfirmed int    `json:"indexers_confirmed"`
}

// FlagResult is what the daemon's shared Flag path returns.
type FlagResult struct {
	InfoHash        string
	IndexersFlagged int
	Attribution     string // targeted | trusted-exempt | none
}

// FlagResponse is the POST /flag document. indexers_flagged is NOT omitempty
// so a client can tell 0-demoted from absent, and attribution lets every
// surface render the honest "no reputations changed" message (the §6 fix).
type FlagResponse struct {
	OK              bool   `json:"ok"`
	InfoHash        string `json:"infohash"`
	IndexersFlagged int    `json:"indexers_flagged"`
	Attribution     string `json:"attribution"`
}

// AggregateBootstrap holds the deny-by-default admission counts, letting an
// operator tell a starved node (nothing admitted) from a quiet one.
type AggregateBootstrap struct {
	Anchors  int `json:"anchors"`
	Admitted int `json:"admitted"`
	Pending  int `json:"pending"`
}

// AggregateStatusResponse is the GET /aggregate document. The services field
// is the LIVE 64-bit sn_search mask (16 lowercase hex, big-endian, no 0x),
// filled by the server from ServicesReporter — never a static constant (the
// §6 static-0x2ED fix).
type AggregateStatusResponse struct {
	KnownIndexers  int                `json:"known_indexers"`
	Services       string             `json:"services"`
	Bootstrap      AggregateBootstrap `json:"bootstrap"`
	CacheSize      int                `json:"cache_size"`     // signed records held for reconciliation
	Reconciliation bool               `json:"reconciliation"` // whether Aggregate sync is advertised
}

// SharingPrefs is the operator-controlled half of the sn_search capability
// set. ShareLocal is a tri-state (0 don't answer / 1 in-swarm only / 2 full
// local index); FileHits/ContentHits are on/off. There is deliberately NO
// publisher field here — it is a daemon-owned runtime fact (the §6 clobber
// fix: an operator "save sharing" cannot reach it).
type SharingPrefs struct {
	ShareLocal  int
	FileHits    bool
	ContentHits bool
}

// CapabilitiesResponse is the GET /capabilities document. publisher and
// services are READ-ONLY: publisher is the daemon-owned Publishing runtime
// fact; services is the live mask so the operator-vs-daemon split is visible.
type CapabilitiesResponse struct {
	ShareLocal  int    `json:"share_local"`
	FileHits    bool   `json:"file_hits"`
	ContentHits bool   `json:"content_hits"`
	Publisher   bool   `json:"publisher"`
	Services    string `json:"services"`
}

// CapabilitiesPatch is the PATCH/POST /capabilities request body. Every field
// is a pointer so an absent field leaves that pref untouched (preserve-unset
// merge, mirroring /config/rate-limit). There is NO publisher field: the
// daemon-owned bit is unrepresentable in the setter path, so a partial save
// can never clobber it.
type CapabilitiesPatch struct {
	ShareLocal  *int  `json:"share_local"`
	FileHits    *bool `json:"file_hits"`
	ContentHits *bool `json:"content_hits"`
}

// IndexStats is the GET /index/stats document.
type IndexStats struct {
	DirBytes        int64   `json:"dir_bytes"`
	DocCount        uint64  `json:"doc_count"`
	TorrentCount    uint64  `json:"torrent_count"`
	ContentCount    uint64  `json:"content_count"`
	CorpusTextBytes int64   `json:"corpus_text_bytes"`
	InflationRatio  float64 `json:"inflation_ratio"`
}
