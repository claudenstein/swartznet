package ltepwire

// This file adds the RIBLT Aggregate set-reconciliation frames (msg_types 4–8)
// to the frozen sn_search envelope. Like the msg 0–3 bodies, each is a single
// bencoded dict with a sorted-key canonical encoding; the keys, caps, and the
// element_size==32 invariant are wire contract. Sync frames are gated on
// services bit 9 (BitSetReconciliation) by the handler.

import (
	"fmt"

	"github.com/anacrolix/torrent/bencode"
)

// Per-message caps and default budgets (frozen).
const (
	MaxSymbolsPerMessage  = 100
	MaxRecordsPerMessage  = 500
	MaxNeedIDsPerMessage  = 1000
	DefaultSyncMaxSymbols = 2000
	DefaultSyncMaxBytes   = 1 << 20
	RIBLTElementSize      = 32
	SyncAlgo              = "riblt-v1"
)

// Sync end status vocabulary.
const (
	SyncStatusConverged     = "converged"
	SyncStatusLimitExceeded = "limit_exceeded"
	SyncStatusAborted       = "aborted"
)

// SyncFilter selects which records a responder reconciles. Zero = all
// publishers, no time floor, no prefix.
type SyncFilter struct {
	Pubkeys [][]byte `bencode:"pubkeys,omitempty"` // each 32 bytes
	Since   int64    `bencode:"since,omitempty"`   // inclusive unix floor
	Prefix  string   `bencode:"prefix,omitempty"`  // keyword prefix
}

// SyncBegin is msg_type 4. Filter is NOT omitempty (an empty filter encodes as
// an empty dict).
type SyncBegin struct {
	MsgType     int        `bencode:"msg_type"`
	TxID        uint32     `bencode:"txid"`
	Algo        string     `bencode:"algo"`
	ElementSize int        `bencode:"element_size"`
	Filter      SyncFilter `bencode:"filter"`
	LocalCount  int        `bencode:"local_count"`
	MaxSymbols  int        `bencode:"max_symbols,omitempty"`
	MaxBytes    int        `bencode:"max_bytes,omitempty"`
}

// SyncSymbol is one RIBLT coded symbol on the wire: c=Count (signed), h=KeyXOR
// (unsigned uint64), b=DataXOR (exactly 32 bytes).
type SyncSymbol struct {
	C int32  `bencode:"c"`
	H uint64 `bencode:"h"`
	B []byte `bencode:"b"`
}

// SyncSymbols is msg_type 5. Index = the stream position of the first symbol.
type SyncSymbols struct {
	MsgType int          `bencode:"msg_type"`
	TxID    uint32       `bencode:"txid"`
	Index   int          `bencode:"index"`
	Symbols []SyncSymbol `bencode:"symbols"`
	Done    bool         `bencode:"done,omitempty"`
}

// SyncNeed is msg_type 6. Empty ids = "done decoding".
type SyncNeed struct {
	MsgType int      `bencode:"msg_type"`
	TxID    uint32   `bencode:"txid"`
	IDs     [][]byte `bencode:"ids"` // each 32 bytes
}

// SyncRecord is one signed record on the wire.
type SyncRecord struct {
	Pk  []byte `bencode:"pk"` // 32 bytes
	Kw  string `bencode:"kw"` // ≤64 bytes
	Ih  []byte `bencode:"ih"` // 20 bytes
	T   int64  `bencode:"t"`
	Pow uint64 `bencode:"pow"`
	Sig []byte `bencode:"sig"` // 64 bytes
}

// SyncRecords is msg_type 7. missing = ids the sender also lacks.
type SyncRecords struct {
	MsgType int          `bencode:"msg_type"`
	TxID    uint32       `bencode:"txid"`
	Records []SyncRecord `bencode:"records"`
	Missing [][]byte     `bencode:"missing,omitempty"`
}

// SyncEnd is msg_type 8.
type SyncEnd struct {
	MsgType   int    `bencode:"msg_type"`
	TxID      uint32 `bencode:"txid"`
	Status    string `bencode:"status"`
	Decoded   int    `bencode:"decoded,omitempty"`
	Sent      int    `bencode:"sent,omitempty"`
	BytesIn   int    `bencode:"bytes_in,omitempty"`
	BytesOut  int    `bencode:"bytes_out,omitempty"`
	AbortCode int    `bencode:"abort_code,omitempty"` // reserved; never populated yet
}

// ---- encoders (stamp msg_type + validate caps) ----

// EncodeSyncBegin stamps algo/element_size defaults and marshals.
func EncodeSyncBegin(m SyncBegin) ([]byte, error) {
	m.MsgType = MsgTypeSyncBegin
	if m.Algo == "" {
		m.Algo = SyncAlgo
	}
	if m.ElementSize == 0 {
		m.ElementSize = RIBLTElementSize
	}
	return bencode.Marshal(m)
}

// EncodeSyncSymbols errors on an empty or over-cap symbol list.
func EncodeSyncSymbols(m SyncSymbols) ([]byte, error) {
	m.MsgType = MsgTypeSyncSymbols
	if len(m.Symbols) < 1 || len(m.Symbols) > MaxSymbolsPerMessage {
		return nil, fmt.Errorf("ltepwire: sync_symbols has %d symbols (want 1..%d)", len(m.Symbols), MaxSymbolsPerMessage)
	}
	for _, s := range m.Symbols {
		if len(s.B) != RIBLTElementSize {
			return nil, fmt.Errorf("ltepwire: sync symbol b must be 32 bytes, got %d", len(s.B))
		}
	}
	return bencode.Marshal(m)
}

// EncodeSyncNeed errors on an over-cap or wrong-length id list.
func EncodeSyncNeed(m SyncNeed) ([]byte, error) {
	m.MsgType = MsgTypeSyncNeed
	if len(m.IDs) > MaxNeedIDsPerMessage {
		return nil, fmt.Errorf("ltepwire: sync_need has %d ids (max %d)", len(m.IDs), MaxNeedIDsPerMessage)
	}
	for _, id := range m.IDs {
		if len(id) != 32 {
			return nil, fmt.Errorf("ltepwire: sync_need id must be 32 bytes, got %d", len(id))
		}
	}
	if m.IDs == nil {
		m.IDs = [][]byte{}
	}
	return bencode.Marshal(m)
}

// EncodeSyncRecords errors on an over-cap list or a malformed record.
func EncodeSyncRecords(m SyncRecords) ([]byte, error) {
	m.MsgType = MsgTypeSyncRecords
	if len(m.Records) > MaxRecordsPerMessage {
		return nil, fmt.Errorf("ltepwire: sync_records has %d records (max %d)", len(m.Records), MaxRecordsPerMessage)
	}
	for _, r := range m.Records {
		if err := validSyncRecord(r); err != nil {
			return nil, err
		}
	}
	if m.Records == nil {
		m.Records = []SyncRecord{}
	}
	return bencode.Marshal(m)
}

// EncodeSyncEnd defaults status to "converged".
func EncodeSyncEnd(m SyncEnd) ([]byte, error) {
	m.MsgType = MsgTypeSyncEnd
	if m.Status == "" {
		m.Status = SyncStatusConverged
	}
	return bencode.Marshal(m)
}

func validSyncRecord(r SyncRecord) error {
	switch {
	case len(r.Pk) != 32:
		return fmt.Errorf("ltepwire: sync record pk must be 32 bytes, got %d", len(r.Pk))
	case len(r.Ih) != 20:
		return fmt.Errorf("ltepwire: sync record ih must be 20 bytes, got %d", len(r.Ih))
	case len(r.Sig) != 64:
		return fmt.Errorf("ltepwire: sync record sig must be 64 bytes, got %d", len(r.Sig))
	case len(r.Kw) < 1 || len(r.Kw) > 64:
		return fmt.Errorf("ltepwire: sync record kw must be 1..64 bytes, got %d", len(r.Kw))
	}
	return nil
}

// ---- decoders (re-assert msg_type + re-validate) ----

// DecodeSyncBegin re-asserts msg_type and the element_size==32 invariant.
func DecodeSyncBegin(payload []byte) (SyncBegin, error) {
	var m SyncBegin
	if err := decodeBounded(payload, &m); err != nil {
		return SyncBegin{}, err
	}
	if m.MsgType != MsgTypeSyncBegin {
		return SyncBegin{}, fmt.Errorf("ltepwire: not a sync_begin, msg_type=%d", m.MsgType)
	}
	if m.ElementSize != RIBLTElementSize {
		return SyncBegin{}, fmt.Errorf("ltepwire: sync_begin element_size=%d, want %d", m.ElementSize, RIBLTElementSize)
	}
	return m, nil
}

// DecodeSyncSymbols re-asserts msg_type and the symbol caps/sizes.
func DecodeSyncSymbols(payload []byte) (SyncSymbols, error) {
	var m SyncSymbols
	if err := decodeBounded(payload, &m); err != nil {
		return SyncSymbols{}, err
	}
	if m.MsgType != MsgTypeSyncSymbols {
		return SyncSymbols{}, fmt.Errorf("ltepwire: not a sync_symbols, msg_type=%d", m.MsgType)
	}
	if len(m.Symbols) < 1 || len(m.Symbols) > MaxSymbolsPerMessage {
		return SyncSymbols{}, fmt.Errorf("ltepwire: sync_symbols has %d symbols", len(m.Symbols))
	}
	for _, s := range m.Symbols {
		if len(s.B) != RIBLTElementSize {
			return SyncSymbols{}, fmt.Errorf("ltepwire: sync symbol b must be 32 bytes, got %d", len(s.B))
		}
	}
	return m, nil
}

// DecodeSyncNeed re-asserts msg_type and the id caps/sizes.
func DecodeSyncNeed(payload []byte) (SyncNeed, error) {
	var m SyncNeed
	if err := decodeBounded(payload, &m); err != nil {
		return SyncNeed{}, err
	}
	if m.MsgType != MsgTypeSyncNeed {
		return SyncNeed{}, fmt.Errorf("ltepwire: not a sync_need, msg_type=%d", m.MsgType)
	}
	if len(m.IDs) > MaxNeedIDsPerMessage {
		return SyncNeed{}, fmt.Errorf("ltepwire: sync_need has %d ids", len(m.IDs))
	}
	for _, id := range m.IDs {
		if len(id) != 32 {
			return SyncNeed{}, fmt.Errorf("ltepwire: sync_need id must be 32 bytes, got %d", len(id))
		}
	}
	return m, nil
}

// DecodeSyncRecords re-asserts msg_type, the record cap, and per-record shape
// (incl. kw≤64).
func DecodeSyncRecords(payload []byte) (SyncRecords, error) {
	var m SyncRecords
	if err := decodeBounded(payload, &m); err != nil {
		return SyncRecords{}, err
	}
	if m.MsgType != MsgTypeSyncRecords {
		return SyncRecords{}, fmt.Errorf("ltepwire: not a sync_records, msg_type=%d", m.MsgType)
	}
	if len(m.Records) > MaxRecordsPerMessage {
		return SyncRecords{}, fmt.Errorf("ltepwire: sync_records has %d records", len(m.Records))
	}
	for _, r := range m.Records {
		if err := validSyncRecord(r); err != nil {
			return SyncRecords{}, err
		}
	}
	return m, nil
}

// DecodeSyncEnd re-asserts msg_type.
func DecodeSyncEnd(payload []byte) (SyncEnd, error) {
	var m SyncEnd
	if err := decodeBounded(payload, &m); err != nil {
		return SyncEnd{}, err
	}
	if m.MsgType != MsgTypeSyncEnd {
		return SyncEnd{}, fmt.Errorf("ltepwire: not a sync_end, msg_type=%d", m.MsgType)
	}
	return m, nil
}
