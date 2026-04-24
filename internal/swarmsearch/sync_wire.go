// sn_search set-reconciliation wire format — msg_types 4..8.
//
// Spec: docs/research/SPEC.md §2. Each message is a bencoded dict
// with a top-level `msg_type` discriminator, identical in shape
// to the existing v1 messages (Query / Result / Reject /
// PeerAnnounce). Capability gate: peers MUST advertise
// BitSetReconciliation (bit 9) in peer_announce before sending
// or receiving any of these.

package swarmsearch

import (
	"fmt"

	"github.com/anacrolix/torrent/bencode"
)

// Sync message-type discriminators.
const (
	MsgTypeSyncBegin   = 4
	MsgTypeSyncSymbols = 5
	MsgTypeSyncNeed    = 6
	MsgTypeSyncRecords = 7
	MsgTypeSyncEnd     = 8
)

// SyncEnd.Status values.
const (
	SyncStatusConverged      = "converged"
	SyncStatusLimitExceeded  = "limit_exceeded"
	SyncStatusAborted        = "aborted"
)

// Defaults tuned for the SPEC §2.9 rate-limit policy.
const (
	// DefaultSyncMaxSymbols is the per-session symbol budget —
	// sender and receiver will abort the session with
	// `limit_exceeded` if this many symbols flow without
	// converging.
	DefaultSyncMaxSymbols = 2000

	// DefaultSyncMaxBytes caps total bulk records transferred in
	// one session. 1 MiB (SPEC §2.9 default).
	DefaultSyncMaxBytes = 1 << 20

	// MaxSymbolsPerMessage bounds one sync_symbols frame's
	// symbol count to keep LTEP extended-message sizes bounded.
	MaxSymbolsPerMessage = 100

	// MaxRecordsPerMessage bounds one sync_records frame.
	MaxRecordsPerMessage = 500

	// MaxNeedIDsPerMessage bounds one sync_need frame.
	MaxNeedIDsPerMessage = 1000
)

// SyncFilter narrows the set of records exchanged in one session.
// All fields are optional; nil pubkey list means "every publisher
// I know about" (server-side interpretation).
type SyncFilter struct {
	Pubkeys [][]byte `bencode:"pubkeys,omitempty"` // 32-byte ed25519 keys
	Since   int64    `bencode:"since,omitempty"`   // unix ts floor
	Prefix  string   `bencode:"prefix,omitempty"`  // keyword prefix
}

// SyncBegin (msg_type 4) opens a sync session. Initiator → responder.
type SyncBegin struct {
	MsgType     int        `bencode:"msg_type"`
	TxID        uint32     `bencode:"txid"`
	Algo        string     `bencode:"algo"`   // "riblt-v1" today
	Filter      SyncFilter `bencode:"filter"`
	ElementSize int        `bencode:"element_size"` // 32
	LocalCount  int        `bencode:"local_count"`  // sender's set size hint
	MaxSymbols  int        `bencode:"max_symbols,omitempty"`
	MaxBytes    int        `bencode:"max_bytes,omitempty"`
}

// SyncSymbol is one wire-level RIBLT symbol per SPEC §2.5.
type SyncSymbol struct {
	Count   int32  `bencode:"c"`
	KeyXOR  uint64 `bencode:"h"`
	DataXOR []byte `bencode:"b"` // always 32 bytes
}

// SyncSymbols (msg_type 5) carries a batch of RIBLT symbols.
// Either direction.
type SyncSymbols struct {
	MsgType int          `bencode:"msg_type"`
	TxID    uint32       `bencode:"txid"`
	Symbols []SyncSymbol `bencode:"symbols"`
	Done    int          `bencode:"done,omitempty"` // 1 = sender has no more
	Index   uint32       `bencode:"index"`          // first symbol's position
}

// SyncNeed (msg_type 6): after RIBLT peeling, a peer lists
// element IDs it discovered in the difference but doesn't have
// records for. The peer responds with SyncRecords. A zero-length
// ids list signals "I'm done decoding from my side"; if both
// sides send that, the session is complete.
type SyncNeed struct {
	MsgType int      `bencode:"msg_type"`
	TxID    uint32   `bencode:"txid"`
	IDs     [][]byte `bencode:"ids"` // each 32 bytes
}

// SyncRecord matches companion.Record on the wire. We duplicate
// the shape here (instead of importing companion) to keep
// swarmsearch's wire package self-contained — the receiver
// re-verifies the signature against the embedded pubkey anyway,
// so the companion package doesn't need to be in the call path.
type SyncRecord struct {
	Pk  []byte `bencode:"pk"`
	Kw  string `bencode:"kw"`
	Ih  []byte `bencode:"ih"`
	T   int64  `bencode:"t"`
	Pow uint64 `bencode:"pow"`
	Sig []byte `bencode:"sig"`
}

// SyncRecords (msg_type 7) bulk-delivers the records listed in a
// preceding sync_need. `missing` carries IDs the sender doesn't
// have either (race: records deleted/expired since SyncSymbols).
type SyncRecords struct {
	MsgType int          `bencode:"msg_type"`
	TxID    uint32       `bencode:"txid"`
	Records []SyncRecord `bencode:"records"`
	Missing [][]byte     `bencode:"missing,omitempty"` // IDs sender lacks
}

// SyncEnd (msg_type 8) terminates a session. Either direction.
type SyncEnd struct {
	MsgType   int    `bencode:"msg_type"`
	TxID      uint32 `bencode:"txid"`
	Status    string `bencode:"status"`
	Decoded   int    `bencode:"decoded,omitempty"`
	Sent      int    `bencode:"sent,omitempty"`
	BytesIn   int    `bencode:"bytes_in,omitempty"`
	BytesOut  int    `bencode:"bytes_out,omitempty"`
	AbortCode int    `bencode:"abort_code,omitempty"`
}

// EncodeSyncBegin serialises a SyncBegin frame.
func EncodeSyncBegin(m SyncBegin) ([]byte, error) {
	m.MsgType = MsgTypeSyncBegin
	if m.ElementSize == 0 {
		m.ElementSize = 32
	}
	if m.Algo == "" {
		m.Algo = "riblt-v1"
	}
	return bencode.Marshal(m)
}

// DecodeSyncBegin parses a SyncBegin frame.
func DecodeSyncBegin(payload []byte) (SyncBegin, error) {
	var m SyncBegin
	if err := bencode.Unmarshal(payload, &m); err != nil {
		return m, fmt.Errorf("swarmsearch: decode sync_begin: %w", err)
	}
	if m.MsgType != MsgTypeSyncBegin {
		return m, fmt.Errorf("swarmsearch: not a sync_begin, msg_type=%d", m.MsgType)
	}
	if m.ElementSize != 32 {
		return m, fmt.Errorf("swarmsearch: unsupported element_size %d", m.ElementSize)
	}
	return m, nil
}

// EncodeSyncSymbols serialises a SyncSymbols frame. Returns an
// error for empty symbol lists or oversized batches.
func EncodeSyncSymbols(m SyncSymbols) ([]byte, error) {
	m.MsgType = MsgTypeSyncSymbols
	if len(m.Symbols) == 0 {
		return nil, fmt.Errorf("swarmsearch: sync_symbols needs ≥1 symbol")
	}
	if len(m.Symbols) > MaxSymbolsPerMessage {
		return nil, fmt.Errorf("swarmsearch: %d symbols exceeds per-message cap %d",
			len(m.Symbols), MaxSymbolsPerMessage)
	}
	for i, s := range m.Symbols {
		if len(s.DataXOR) != 32 {
			return nil, fmt.Errorf("swarmsearch: symbol[%d] DataXOR %d bytes, want 32",
				i, len(s.DataXOR))
		}
	}
	return bencode.Marshal(m)
}

// DecodeSyncSymbols parses a SyncSymbols frame and enforces the
// same size invariants.
func DecodeSyncSymbols(payload []byte) (SyncSymbols, error) {
	var m SyncSymbols
	if err := bencode.Unmarshal(payload, &m); err != nil {
		return m, fmt.Errorf("swarmsearch: decode sync_symbols: %w", err)
	}
	if m.MsgType != MsgTypeSyncSymbols {
		return m, fmt.Errorf("swarmsearch: not sync_symbols, msg_type=%d", m.MsgType)
	}
	if len(m.Symbols) == 0 {
		return m, fmt.Errorf("swarmsearch: sync_symbols has zero symbols")
	}
	if len(m.Symbols) > MaxSymbolsPerMessage {
		return m, fmt.Errorf("swarmsearch: sync_symbols has %d symbols (cap %d)",
			len(m.Symbols), MaxSymbolsPerMessage)
	}
	for i, s := range m.Symbols {
		if len(s.DataXOR) != 32 {
			return m, fmt.Errorf("swarmsearch: symbol[%d] DataXOR %d bytes", i, len(s.DataXOR))
		}
	}
	return m, nil
}

// EncodeSyncNeed serialises a SyncNeed frame.
func EncodeSyncNeed(m SyncNeed) ([]byte, error) {
	m.MsgType = MsgTypeSyncNeed
	if len(m.IDs) > MaxNeedIDsPerMessage {
		return nil, fmt.Errorf("swarmsearch: sync_need has %d ids (cap %d)",
			len(m.IDs), MaxNeedIDsPerMessage)
	}
	for i, id := range m.IDs {
		if len(id) != 32 {
			return nil, fmt.Errorf("swarmsearch: sync_need id[%d] %d bytes, want 32", i, len(id))
		}
	}
	if m.IDs == nil {
		m.IDs = [][]byte{}
	}
	return bencode.Marshal(m)
}

// DecodeSyncNeed parses a SyncNeed frame.
func DecodeSyncNeed(payload []byte) (SyncNeed, error) {
	var m SyncNeed
	if err := bencode.Unmarshal(payload, &m); err != nil {
		return m, fmt.Errorf("swarmsearch: decode sync_need: %w", err)
	}
	if m.MsgType != MsgTypeSyncNeed {
		return m, fmt.Errorf("swarmsearch: not sync_need, msg_type=%d", m.MsgType)
	}
	if len(m.IDs) > MaxNeedIDsPerMessage {
		return m, fmt.Errorf("swarmsearch: sync_need has %d ids (cap %d)",
			len(m.IDs), MaxNeedIDsPerMessage)
	}
	for i, id := range m.IDs {
		if len(id) != 32 {
			return m, fmt.Errorf("swarmsearch: sync_need id[%d] %d bytes", i, len(id))
		}
	}
	return m, nil
}

// EncodeSyncRecords serialises a SyncRecords frame. Rejects
// malformed records (wrong sizes) before emitting.
func EncodeSyncRecords(m SyncRecords) ([]byte, error) {
	m.MsgType = MsgTypeSyncRecords
	if len(m.Records) > MaxRecordsPerMessage {
		return nil, fmt.Errorf("swarmsearch: sync_records has %d records (cap %d)",
			len(m.Records), MaxRecordsPerMessage)
	}
	for i, r := range m.Records {
		if len(r.Pk) != 32 {
			return nil, fmt.Errorf("swarmsearch: record[%d].pk %d bytes", i, len(r.Pk))
		}
		if len(r.Ih) != 20 {
			return nil, fmt.Errorf("swarmsearch: record[%d].ih %d bytes", i, len(r.Ih))
		}
		if len(r.Sig) != 64 {
			return nil, fmt.Errorf("swarmsearch: record[%d].sig %d bytes", i, len(r.Sig))
		}
	}
	if m.Records == nil {
		m.Records = []SyncRecord{}
	}
	return bencode.Marshal(m)
}

// DecodeSyncRecords parses a SyncRecords frame and validates
// the per-record byte lengths.
func DecodeSyncRecords(payload []byte) (SyncRecords, error) {
	var m SyncRecords
	if err := bencode.Unmarshal(payload, &m); err != nil {
		return m, fmt.Errorf("swarmsearch: decode sync_records: %w", err)
	}
	if m.MsgType != MsgTypeSyncRecords {
		return m, fmt.Errorf("swarmsearch: not sync_records, msg_type=%d", m.MsgType)
	}
	if len(m.Records) > MaxRecordsPerMessage {
		return m, fmt.Errorf("swarmsearch: sync_records has %d records (cap %d)",
			len(m.Records), MaxRecordsPerMessage)
	}
	for i, r := range m.Records {
		if len(r.Pk) != 32 {
			return m, fmt.Errorf("swarmsearch: record[%d].pk %d bytes", i, len(r.Pk))
		}
		if len(r.Ih) != 20 {
			return m, fmt.Errorf("swarmsearch: record[%d].ih %d bytes", i, len(r.Ih))
		}
		if len(r.Sig) != 64 {
			return m, fmt.Errorf("swarmsearch: record[%d].sig %d bytes", i, len(r.Sig))
		}
	}
	return m, nil
}

// EncodeSyncEnd serialises a SyncEnd frame.
func EncodeSyncEnd(m SyncEnd) ([]byte, error) {
	m.MsgType = MsgTypeSyncEnd
	if m.Status == "" {
		m.Status = SyncStatusConverged
	}
	return bencode.Marshal(m)
}

// DecodeSyncEnd parses a SyncEnd frame.
func DecodeSyncEnd(payload []byte) (SyncEnd, error) {
	var m SyncEnd
	if err := bencode.Unmarshal(payload, &m); err != nil {
		return m, fmt.Errorf("swarmsearch: decode sync_end: %w", err)
	}
	if m.MsgType != MsgTypeSyncEnd {
		return m, fmt.Errorf("swarmsearch: not sync_end, msg_type=%d", m.MsgType)
	}
	return m, nil
}
