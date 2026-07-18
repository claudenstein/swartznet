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
