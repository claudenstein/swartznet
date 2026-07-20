package swarmsearch

import "time"

// LocalHit mirrors indexer.SearchHit field-for-field so this package answers
// inbound queries WITHOUT importing internal/indexer (the Layer-L/Layer-S
// isolation law). The engine adapts *indexer.Index → LocalSearcher.
type LocalHit struct {
	DocType   string    // "torrent" | "content"
	InfoHash  string    // 40-char lowercase hex (SHA-1)
	Name      string    // torrent name
	SizeBytes int64     // torrent size
	AddedAt   time.Time // zero ⇒ no freshness stamp on the wire (§6 defect b)
	Seeders   int
	Leechers  int
	FileIndex int    // for content hits
	FilePath  string // for content hits
	Score     float64
}

// LocalSearcher answers inbound sn_search queries against the local index. A
// nil searcher makes every inbound query reject with RejectShuttingDown.
type LocalSearcher interface {
	SearchLocal(query string, limit int) (total int, hits []LocalHit, err error)
}

// IndexerSink receives a publisher pubkey gossiped in a peer_announce, for the
// Layer-D indexer set. Wired as a no-op stub in Slice 7 (dhtindex is later).
type IndexerSink interface {
	NoteGossipIndexer(pubkey [32]byte, label string)
}

// EndorsementSink receives an endorsement (endorser vouches for candidate) from
// a peer_announce, for bootstrap admission. No-op stub in Slice 7.
type EndorsementSink interface {
	NoteEndorsement(endorser, candidate [32]byte)
}

// PeerToken authorizes an sn_search send to a peer that advertised the
// extension in its LTEP `m` dict. Its fields are unexported and it has no
// exported constructor: the ONLY way to obtain one is for the Protocol to mint
// it from a recorded advertisement (OnRemoteHandshake). This makes "you cannot
// send sn_search to a peer that never advertised it" a compile-time property —
// there is no bare-address send API.
type PeerToken struct {
	addr  string
	epoch uint64
}

// Addr is the remote address the token authorizes. The engine's Transport
// implementation reads it to resolve the live connection.
func (t PeerToken) Addr() string { return t.addr }

// valid reports whether the token was minted (a zero token authorizes nothing).
func (t PeerToken) valid() bool { return t.addr != "" }

// Transport is the engine-supplied sender. It takes a PeerToken, never a bare
// address, so a caller physically cannot address a peer that did not advertise.
type Transport interface {
	SendExtension(peer PeerToken, frame []byte) error
}
